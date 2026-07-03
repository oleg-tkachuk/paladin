# ADR-0011: Per-tenant physical S3 bucket layout (hybrid with the shared layout)

- **Status:** Proposed 2026-07-02 — design ratified on explicit request (the
  BACKLOG entry *"Per-tenant S3 bucket layout"* was held **do-not-design until
  the user signals go**; that signal was given). **No implementation has
  landed.** This ADR records the design so that when the product green-light to
  build arrives it can be executed coherently and in phases, rather than
  piecemeal. The *decision to roll it out* remains product-gated; the
  *architecture* is settled here.

- **Context.** Every tenant's objects land inside a **shared** physical S3
  bucket today, isolated by a key prefix. The four-level hierarchy already in
  the schema is:

  ```
  storage_backends  platform-global cluster; endpoint/region/creds-ref
    └─ buckets       PK(backend_id, bucket_name); owner_tenant_id NULL=shared / UUID=dedicated
       │             provision_state: pending→ready→failed / deleting  (outbox-reconciled)
       └─ object_keys PK(tenant_id, object_key); FK→(backend_id,bucket_name); tenancy trigger
          └─ objects  physical S3 key = <tenant_id>/<object_key>/<key>
  ```

  Tenant isolation is **already** enforced by three independent mechanisms, and
  this is the crux of the design — per-tenant buckets are not a new
  architecture, they are the population of fields that already exist:

  | Mechanism | Where | Effect |
  |---|---|---|
  | Key prefix `<tenant_id>/…` | [`s3adapter/s3.go`](../../internal/storage/s3adapter/s3.go) `composeKey` | every object's S3 key is tenant-rooted |
  | `buckets.owner_tenant_id` + trigger | [`migrations/006_v2_planes.sql`](../../migrations/006_v2_planes.sql) `enforce_object_key_bucket_tenancy` | NULL ⇒ any tenant may bind; set ⇒ only the owner may |
  | Per-object_key bucket binding | `object_keys.(backend_id, bucket_name)` FK | each namespace names its own physical bucket |

  Concrete state discovered while designing this (do not re-derive):

  - **Physical bucket provisioning already exists.** `CreateBucket` accepts a
    `ProvisionOnBackend` flag → writes `provision_state='pending'`; the
    [`BucketReconciler`](../../internal/worker/bucket_reconciler.go) worker
    calls `provisioner.CreateBucket` / `DeleteBucket` idempotently via the
    outbox. `owner_tenant_id`, `region`, and `tenant_default_bindings`
    (tenant → default `(backend_id, bucket_name)`) are all present.
  - **Provisioning is NOT wired into CreateTenant.** A tenant is created
    logical-only ([`internal/api/v1/tenant/handler.go`](../../internal/api/v1/tenant/handler.go));
    nothing provisions or binds a bucket at tenant-create time.
  - **One S3 client per process.** `build_deps.go` builds a single
    `s3adapter.New(ctx, backend)` from `config.Storage.DefaultBackend`;
    `resolveBucket(perCall)` swaps only the bucket **name**, reusing one
    endpoint/credentials/region. `provisioner.CreateBucket(backendID, …)` even
    ignores `backendID` for client selection. So a dedicated bucket on the
    **same** backend works on today's wiring; a dedicated bucket on a
    **different** backend/region/account does not.
  - The read/presign hot path is already **layout-agnostic** below
    `LookupBucket` — it receives a `(backend_id, bucket_name)` and composes the
    tenant-rooted key regardless of whether the bucket is shared or dedicated.

## Decision

Model storage layout as a **per-tenant** property and support `shared` and
`dedicated` **side by side** (hybrid). Do not replace the shared layout;
add dedicated as an opt-in tier.

- **`shared`** (default; today's behavior): object_keys bind to a bucket with
  `owner_tenant_id IS NULL`.
- **`dedicated`**: the tenant owns one physical bucket
  (`owner_tenant_id = tenant`), provisioned on a backend; its object_keys bind
  only there (the existing trigger already enforces this).

Four invariants make the hybrid safe and cheap:

1. **Uniform keys.** The physical key stays `<tenant_id>/<object_key>/<key>` in
   **both** layouts. The `<tenant_id>/` prefix is redundant inside a dedicated
   bucket but is kept, because it makes a shared→dedicated move a pure
   `CopyObject` with an **identical** key and keeps every code path below
   bucket-resolution layout-agnostic. `composeKey` is never forked.
2. **Layout decided at bind, not at read.** Which bucket an object_key lands on
   is a provisioning/CreateObjectKey concern. `LookupBucket` returns whatever
   the object_key is bound to; the hot path does not branch on layout.
3. **Enforcement stays on `owner_tenant_id`.** The new `storage_layout` field
   (below) is intent/policy only; the DB trigger remains the sole isolation
   gate, so a dedicated tenant physically cannot bind to another bucket.
4. **Hybrid is mandatory, not transitional.** Dedicated is a tier for
   high-value / compliance / cost-attributed tenants; the long tail stays
   shared. This is forced by the S3 bucket-quota ceiling (see Consequences) —
   "a bucket per tenant" does not scale to 100k tenants on one account.

### Schema delta (minimal — most fields already exist)

```sql
-- Intent/policy marker; enforcement remains on buckets.owner_tenant_id.
ALTER TABLE tenants ADD COLUMN storage_layout TEXT NOT NULL DEFAULT 'shared'
  CHECK (storage_layout IN ('shared','dedicated'));

-- Gate presign/put on dedicated-bucket readiness (add to LookupBucket's JOIN):
--   ... AND b.provision_state = 'ready'   -- else a retryable "provisioning" error
```

Everything else — `owner_tenant_id`, `provision_state`, `region`,
`tenant_default_bindings` — is reused as-is.

### Phases (staged so value ships before the risky refactor)

- **Phase 1 — dedicated bucket on the same backend (works on today's wiring).**
  `CreateTenant{storage_layout:'dedicated'}` inserts, in the **same
  transaction**, the `tenants` row + a `buckets{owner_tenant_id=tenant,
  provision_state='pending', region}` row + a `tenant_default_bindings` row, and
  fans out `paladin.tenant.created` + `paladin.bucket.created` on the outbox. The
  existing `BucketReconciler` provisions the physical bucket. Presign/put gate
  on `provision_state='ready'` with a retryable error. **No S3-client change.**
- **Phase 2 — multi-backend routing (the real unblock).** Replace the single
  `s3adapter.Client` with a `BackendRegistry` keyed by `backend_id` that lazily
  builds one client per backend from `storage_backends.{endpoint, region,
  public_endpoint, credentials_secret_ref}` — the same pooled-client pattern as
  `SQSClientPool` / `RabbitMQConnPool`. `LookupBucket` already returns
  `backend_id`, so resolution becomes `registry.For(backend_id).Presign(…)`.
  This unblocks per-tenant **region pinning** and per-tenant **IAM** (a distinct
  AssumeRole / credential per backend). Detailed component design:
  [docs/backend-registry.md](../backend-registry.md).
- **Phase 3 — shared→dedicated migration job (reuse StorageReplicator).**
  Because keys are uniform: provision the dedicated bucket → server-side
  `CopyObject` every object under the `<tenant_id>/` prefix with the **same**
  key (the `CopyObject` path already exists) → under a lock, rebind
  `object_keys.(backend_id, bucket_name)` (the FK is
  `DEFERRABLE INITIALLY DEFERRED`, so the swap is transaction-safe) → verify
  counts/checksums → delete the old prefix (or tombstone with a retention
  window). Default policy is **new tenants may be dedicated, legacy stays shared
  forever** (zero migration); the copy job is an explicit per-tenant admin
  action, not automatic.

### Lifecycle answers (the BACKLOG DoD questions)

- **Provisioning is async** (outbox), consistent with the crash-safe pattern
  already in use. The "tenant exists, bucket still provisioning" window is
  closed by the `provision_state='ready'` gate.
- **Naming**: a configurable template defaulting to `{org}-paladin-{tenant_uuid}`.
  S3 bucket names are **globally unique**, so an org/account discriminator is
  mandatory; the tenant UUID satisfies `bucket_name_format` (36 chars ≤ 63).
- **Region pinning**: chosen at CreateTenant, stored in `buckets.region`.
- **Cost attribution** becomes native: tag the bucket at creation
  (`PutBucketTagging tenant_id=<uuid>`) so cloud cost reports group
  bucket→tenant. Shared cannot do this; it is a genuine dedicated-only win. The
  bucket→tenant map already lives in `owner_tenant_id`.
- **Purge**: hard-delete marks the owned bucket `provision_state='deleting'`
  after objects are emptied; the reconciler calls `DeleteBucket`. The existing
  `ON DELETE RESTRICT` FKs already force the correct teardown order.

### What this ADR deliberately does NOT do

- **Does not change the physical key layout** — `<tenant_id>/<object_key>/<key>`
  stays for both layouts (Invariant 1).
- **Does not touch RLS** — the `objects` GUC-keyed tenant isolation
  (`paladin.tenant_id`, migration 023) is orthogonal and covers both layouts.
- **Does not auto-migrate** existing shared tenants.
- **Does not commit to a rollout** — that is product-gated. This ADR is the
  architecture, ready to execute on green-light.

## Implementation status (2026-07-03)

**Phases 1 and 2 are SHIPPED; only Phase 3 (the migration copy job) remains.**

- **Phase 2** landed first as the enabling refactor (#121–#124): the
  `BackendRegistry` (client-per-backend), `backend_id` threaded from the
  bucket resolver to the storage boundary, per-backend routers behind every
  `wire.Storage` interface — proven end-to-end against two physically
  distinct MinIO backends. Follow-ups shipped with it: the maintenance
  workers (bucket reconciler, reconciler probe, hard-deleter, multipart
  reaper) route by backend id (#125–#126), and multipart uploads are
  anchored to their initiate-time `(backend, bucket)` (migration 053, #127),
  closing the `BindObjectKeyToBucket` rebind gap.
- **Phase 1** shipped on top (#128–#130): `tenants.storage_layout`
  (migration 054, proto `Tenant.storage_layout`), provision-on-create — a
  `dedicated` tenant gets a pending tenant-owned bucket + default binding in
  the CreateTenant tx, physically created by the backend-routed reconciler —
  and the mutation gate on `provision_state='ready'`
  (`ErrBucketProvisioning` → FailedPrecondition, retryable).
- **Phase 3** (shared→dedicated copy job + `streamThrough` cross-backend
  copy) is tracked in BACKLOG; the `CopyObject` router refuses cross-backend
  pairs loudly until it lands. Smaller deferrals (per-tenant backend
  selection, org-prefixed bucket names, provision-time cost-attribution
  tagging, frontend field surfacing) are listed there too.

Component-level design for the Phase 2 machinery:
[docs/backend-registry.md](../backend-registry.md).

## Consequences

- **Positive.** Native per-tenant cost attribution; per-tenant IAM blast-radius
  (a leaked credential is contained to one bucket); per-tenant lifecycle /
  region / replication rules; removal of the shared listing-index prefix
  hotspot for dedicated tenants. Shared tenants are unaffected.
- **Breaking.** None for existing tenants — `storage_layout` defaults to
  `shared` and the read path does not change. New behavior is entirely opt-in.
- **Cost.** Phase 1 is nearly free (one column + reuse of the reconciler).
  Phase 2's `BackendRegistry` is the largest single unit but is an isolated,
  well-precedented pattern (SQS/RabbitMQ pools). Ongoing: dedicated buckets
  consume the account's bucket quota (~100 default, raisable to 1000/10k) — the
  hard ceiling that mandates the hybrid tier and, at scale, multi-account
  sharding.
- **Rollback.** Phase 1/2 are additive and flag-free; a dedicated tenant can be
  migrated back to shared by the inverse of the Phase 3 copy job. The
  `storage_layout` column is reversible (Down drops it; enforcement never
  depended on it). No authz-visible change to roll back — the isolation gate
  (`owner_tenant_id` trigger) is unchanged.

## Alternatives considered

- **Keep shared-only.** Rejected as the *default* is fine, but it cannot offer
  per-tenant cost attribution, per-tenant IAM isolation, or per-customer
  lifecycle/region rules — real asks for AWS-native and compliance tenants.
- **Replace shared with dedicated-for-all.** Rejected — the S3 bucket-quota
  ceiling makes one-bucket-per-tenant infeasible past a few hundred tenants
  without multi-account sharding; forcing it on the long tail is pure downside.
  Hence hybrid.
- **Drop the `<tenant_id>/` prefix inside dedicated buckets.** Rejected for v1 —
  it forks `composeKey` by layout and turns shared→dedicated migration into a
  key-rewrite instead of a same-key `CopyObject`, for a cosmetic S3-browsing
  gain.
- **A global `storage_layout` switch instead of per-tenant.** Rejected —
  contradicts the hybrid requirement; layout must be selectable per tenant
  (indeed per object_key, which the schema already permits).
