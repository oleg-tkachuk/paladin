# ADR-0015: Per-tenant physical S3 bucket layout (hybrid with the shared layout)

- **Status:** Accepted — Phases 1 and 2 and the Phase 3 copy job have shipped
  (see *Implementation status* below); what remains of Phase 3 is in
  [`BACKLOG.md`](../../BACKLOG.md). Originally Proposed 2026-07-02 — design ratified on explicit request (the
  BACKLOG entry *"Per-tenant S3 bucket layout"* was held **do-not-design until
  the user signals go**; that signal was given). No implementation had
  landed then. This ADR records the design so that when the product green-light to
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
  | Key prefix `<tenant_id>/…` | [`s3adapter/s3.go`](../../backend/internal/storage/s3adapter/s3.go) `composeKey` | every object's S3 key is tenant-rooted |
  | `buckets.owner_tenant_id` + trigger | [`migrations/003_triggers.sql`](../../backend/migrations/003_triggers.sql) `collections_enforce_bucket_tenancy` | NULL ⇒ any tenant may bind; set ⇒ only the owner may. *Superseded by [ADR-0032](0032-every-bucket-has-a-tenant-owner.md): every bucket has an owner, and only the owner or a tenant it granted the bucket to may bind.* |
  | Per-object_key bucket binding | `object_keys.(backend_id, bucket_name)` FK | each namespace names its own physical bucket |

  Concrete state discovered while designing this (do not re-derive):

  - **Physical bucket provisioning already exists.** `CreateBucket` accepts a
    `ProvisionOnBackend` flag → writes `provision_state='pending'`; the
    [`BucketReconciler`](../../backend/internal/worker/bucket_reconciler.go) worker
    calls `provisioner.CreateBucket` / `DeleteBucket` idempotently via the
    outbox. `owner_tenant_id`, `region`, and `tenant_default_bindings`
    (tenant → default `(backend_id, bucket_name)`) are all present.
  - **Provisioning is NOT wired into CreateTenant.** A tenant is created
    logical-only ([`internal/api/admin/v1/tenanth/handler.go`](../../backend/internal/api/admin/v1/tenanth/handler.go));
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
  `owner_tenant_id IS NULL`. Since [ADR-0032](0032-every-bucket-has-a-tenant-owner.md),
  a bucket the operator owns (the platform tenant) and granted to the tenant.
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
  [docs/backend-registry.md](../../backend/docs/backend-registry.md).
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
  - **A failed verify rolls back** (amended 2026-10). The verify runs after
    the rebind, so a failure finds the tenant already serving from a
    dedicated bucket that is missing objects. Failing in place left it there
    with no repair path and no retry (a retry needs a `shared` tenant).
    Instead the migration moves `verifying → rolling_back`, recording the
    verify error: the worker copies back to the source (same-key
    `CopyObject`, cursor-resumable) every AVAILABLE private object written at
    or after `rebound_at` — the rebind transaction's start, recorded by it —
    measured by `objects.updated_at`, which every byte-changing write to an
    object row moves; then, in ONE transaction under the migration row's lock
    and the state + attempt guard, and only if nothing was written past the
    cursor meanwhile, it rebinds the private collections and the default
    binding to the source bucket, flips the layout back to `shared` and sets
    `failed` with the verify error kept. The tenant is then retried like any
    other failure. Older objects are not copied (their source copy is the
    original; the target copy may be the broken one), nor are rows deleted
    after the rebind (visibility is the row's state, not the bucket's
    contents). The rollback never runs cleanup: the source copies are the
    live data again, and cleanup runs only from `completed`. A migration
    that failed this way before rollbacks existed is rolled back by calling
    `MigrateTenantStorageLayout` on its tenant.

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
  (`ErrBucketProvisioning` → Unavailable with `ERROR_REASON_BUCKET_PROVISIONING`,
  retryable; the state table lives in `bucketgate`).
- **Phase 3** (shared→dedicated copy job + `streamThrough` cross-backend
  copy) is tracked in BACKLOG; the `CopyObject` router refuses cross-backend
  pairs loudly until it lands. Smaller deferrals (per-tenant backend
  selection, org-prefixed bucket names, provision-time cost-attribution
  tagging, frontend field surfacing) are listed there too.

Component-level design for the Phase 2 machinery:
[docs/backend-registry.md](../../backend/docs/backend-registry.md).

## Addendum (2026-11-22): the state gate asks what an operation touches

The mutation gate on `provision_state` grew into one gate over the bucket's
and the backend's state, `bucketgate.Check`. It first classified every
operation as a read or a write, and refused every write on a draining
(read-only) backend. That was the wrong question. A draining backend still
holds and serves its bytes, so refusing a change to a Paladin row there buys
nothing, and refusing `SetObjectTaint` did harm: content flagged as unsafe
stayed readable, and could not be flagged, for the whole drain.

Each operation now names its access (`bucketgate.Access`, a closed set whose
zero value is the strictest):

| Access | What it touches | Disabled | Draining | Bucket pending | Bucket failed / deleting |
|---|---|---|---|---|---|
| `BytesWrite` | bytes on the backend | refused | refused | refused (retryable) | refused |
| `Read` | reads bytes, or resolves a bucket to scope a read | refused | served | served | served |
| `MetadataWrite` | Paladin rows only | refused | served | served | served |
| `Protective` | a control that keeps content from being served | served | served | served | served |

A binding that cannot be read refuses every write, `Protective` included: no
write is authorized on an empty binding.

By RPC:

| Access | RPCs |
|---|---|
| `BytesWrite` | `UploadObject`, `CompleteObject`, `CopyObject` (destination), `DeleteObject` with `permanent`, `RestoreObjectVersion`, presigned PUT, multipart initiate / complete / part URLs, batch copy (destination), permanent batch delete |
| `Read` | `GetObject`, `DownloadObject`, list, presigned GET, `GetObjectLock`, `CopyObject` and batch copy (source), the HEAD `RestoreObject` makes, `AbortMultipartUpload`, `ListParts` |
| `MetadataWrite` | `UpdateObject` and the tag RPCs on it, `DeleteObject` to the trash, `RestoreObject`, `SetObjectRetention`, `SetObjectLegalHold`, the tag, restore and trash batches |
| `Protective` | `SetObjectTaint`, setting or clearing |

What makes the `MetadataWrite` RPCs safe while a backend drains:

- They write Postgres alone. Tags and metadata go through
  `UpdateObjectMetadata`, guarded by `resource_version`; a trash move and a
  restore flip `objects.state`; retention and legal hold are rows in
  `object_locks` (`ObjectLockRepo`), and Paladin never calls S3 Object Lock;
  a taint is `objects.taint`. None calls the storage client, apart from the
  HEAD a restore makes to check the bytes are still there.
- Nothing that moves objects writes object rows, so a change made during a
  move cannot be overwritten. `read_only` is set by an operator and read by
  the gate alone; the background workers that delete bytes ask the gate too
  (addendum below), and none of them moves objects. The one mover is this ADR's storage
  migration. It copies bytes under the same key and rebinds
  `collections.bucket_id`, and the object row, with its tags, locks and
  taint, stays where it was. A restore that lands after the copy has passed
  its key is caught by the physical verify (its `Header` is wired in
  `build_jobs.go`), which rolls the migration back.
- Two gaps in the migration itself do not depend on the gate and are
  recorded in BACKLOG: it leaves the trash behind, and its rollback copies
  back any object whose row changed after the rebind.

A multipart session is decided on the bucket it was anchored to at
initiate, where its parts are, never on the collection's current binding:
`GetSession` reads that bucket's gate state with the session, and every RPC on
the session asks the gate about it (`multiparth`, `target`). After this ADR's
migration rebinds a collection, a session anchored to the draining source is
refused a completion or a part URL there, whatever the new binding's state.
`multipart_uploads.bucket_id` is `NOT NULL`, so there is no unanchored session
to fall back from.

A batch is admitted by the gate at submit and runs later, on a worker. Each
executor asks the gate again, with the batch's access, before its first item
(`internal/worker/operations/bucket_gate.go`), and a refusal fails the
operation with the gate's refusal: its reason is the operation's error code,
answered as the refusal's own code. The batch is not deferred. It continues
one request, whose answer is the refusal, and the runner has no way back to
`PENDING` that would not claim the operation again at once. The gate is asked
once per batch: a backend disabled while a batch runs does not stop it, and
the items written before stay written, as the rows one RPC wrote do.

## Addendum (2026-11-22): background workers ask the same gate

Request paths reach the gate through the bucket resolver; the workers that
delete bytes did not, so a draining backend refused `DeleteObject` with
`permanent` while the lifecycle hard delete and the purge drainer deleted the
same bytes from it once a trashed object's retention ran out. Those workers
now ask `bucketgate.Check` about the bucket of every row they claim, from the
bucket state their own capped claim returns with it, and work only the rows
it admits (`internal/worker/bucket_gate.go`):

| Worker | Access | A refused bucket's work |
|---|---|---|
| `LifecycleHardDeleter` | `BytesWrite` | the row stays in the trash with its bytes, restorable |
| `PurgeDrainer` | `BytesWrite` | the debt stays due, no attempt counted, no backoff grown |
| `MultipartReaper` | `Read` | the session stays open |
| `MultipartAbortDrainer` | `Read` | the debt stays due, no attempt counted |
| `ReconcilerV2` (bytes that broke their registration) | `BytesWrite` | the row stays `PENDING` |
| `ReconcilerV2` (the HEAD, then a promote or a discard) | `Read` | the row stays `PENDING`, not HEADed |
| `LifecycleWorker` (expiry to the trash) | `MetadataWrite` | the bucket is skipped for the tick; its objects stay as they are |

The work is deferred, never failed or dropped, and each sweep logs every
bucket it defers once, at Warn, with the gate's reason.

The reconciler asks as `Read` though `CompleteObject`, which promotes the same
row, is a `BytesWrite`: the bytes are on the backend already, and what the
reconciler writes is the row alone; it settles an upload the client left
rather than taking the client's next step. A disabled backend's pending
objects stay `PENDING`, and the overdue-pending gauge grows with them, until
it is enabled. The lifecycle expiry lists its buckets rather than claiming
rows, so it asks once per bucket and skips a refused one for the tick. The first sweep whose
gate admits the bucket takes the work. A claim that met a newly refused
bucket is followed by one that leaves that bucket's rows out, for the slots
they took: a refused bucket's rows never head every claim and starve the
buckets behind them, and every query stays capped by the batch, so a large
refused backlog costs one claim, never a scan of itself.

**A purge on a draining backend is deferred, not let through.** A drain's
end is an empty backend, and deleting a purged tenant's bytes looks like
progress towards it. It is deferred anyway:

- The gate already answers this operation on the request path: a permanent
  delete is a write of bytes, refused while the backend drains. A trash move
  followed by the hard delete removes the same bytes and must get the same
  answer, or the trash is a way around the drain.
- `read_only` is an operator's promise that the backend's contents hold
  still while something moves them: this ADR's migration, or a copy the
  operator runs outside Paladin. A delete under that copy can remove bytes it
  has already taken, and settling the debt then forgets the copy on the
  target, with nothing left to name it.
- Deferral loses nothing. The debt row is durable, and its bucket cannot be
  deleted under it (`DeleteBucket` refuses a bucket `pending_purges`
  references, and the foreign key is `RESTRICT`).

The cost: a purged tenant's bytes outlive its purge for as long as the drain
runs. A backend whose drain ends in `enabled = false`, not in writes again,
keeps its debts on record; they are paid if it is ever enabled, and they are
the list of what it still holds if it is decommissioned instead.

**An abort differs.** Multipart parts were never an object: no copy of the
backend takes them, and aborting frees storage rather than adding data. The
abort workers ask as `Read`, as `AbortMultipartUpload` does: refused only on
a disabled backend, which serves no Paladin-mediated operation.

**Provision states do not pin the teardown.** `BytesWrite` is refused on a
bucket being deleted, but the bucket delete refuses while any debt names the
bucket, so a bucket never reaches `deleting` with debts its own teardown
would wait on.

Not routed through the gate, deliberately: the bucket reconciler, which
moves `provision_state` itself, so the gate would refuse its own work; and
the storage migration, which is what drains a backend.

## Addendum (2026-11-24): an upload is anchored to the bucket its URL names

The copy skips PENDING objects, and a single-part upload's URL names the
bucket the collection was bound to when it was signed. A PUT made after the
rebind lands in the source, which nothing read again: `CompleteObject` HEADed
the new binding, and the reconciler discarded the row.

The row now records that bucket (`objects.upload_bucket_id`, migration 113),
as a multipart session records its own. `CompleteObject` and the reconciler
look for the bytes there first and, when it is not the collection's binding,
copy them into the binding under the same key before promoting; a URL
regenerated after the rebind names the binding, which is looked in next. The
promote clears the anchor. The anchored copy is not deleted there: the
migration's cleanup removes it with the other source copies, and waits while
any upload anchored to the source is still PENDING. So a crash between the
copy and the promote leaves bytes in both buckets, and a retry copies again.
An operator's direct `BindCollectionToBucket` moves no bytes at all: an
upload completed after one is moved like this, and its anchored copy stays
in the old bucket with every other object's.

## Addendum (2026-11-24): a purge during a migration owes both buckets

The copy, the verify and the cleanup walk object rows; a purge deletes the
row, then the bytes in the bucket the collection is bound to. Purged after
its copy, an object left that copy in the target; purged between the rebind
and cleanup, its source copy. Purges during the copy also put it short of the
count it started from, which failed the migration as `copy incomplete`.

Two ways were open: the purge owes both buckets, or the migration reconciles
each bucket's keys against the rows. The second needs a listing of the
tenant's prefix in both buckets, and its decision — no row, so delete — races
every upload writing that prefix; it is correct only under the path lock, key
by key, over the whole prefix. The first is chosen:

- **The purge's transaction records a debt per bucket** (`purgedebt`,
  `pending_purges.migration_copy`, migration 115), for each bucket of the
  migration other than the bound one, in every state but `cleaned` — a
  failed attempt leaves its copies in the target, through the retry's
  `provisioning` — and only in a ready bucket; the purge drainer deletes and retries them
  as any debt, and announces nothing for them, so `paladin.object.purged`
  still fires once. Recorded with the row's delete, they survive a crash at
  any later step; S3 deletes of an absent key succeed, so a repeat is free.
- **The copy holds each row while it copies it** (`FOR KEY SHARE`) and
  advances its cursor on that transaction. A purge either waits for the copy
  and then owes the copy it made, or ran first and leaves no row to copy. The
  rollback's copy back and the verify's check of a missing object hold the
  row the same way. The hold's transaction is idle while the object copies,
  so it lifts the pool's idle-in-transaction timeout for itself; a purge
  waiting on it is bounded by `lock_timeout` and retried.
- **A purge takes an object the copy has not reached off its total**, on its
  transaction, after the row's delete — the copy's lock order — and the copy
  phase counts its total under the migration row's lock, which a purge takes
  too. A copy short of the total is still one that missed rows.

Bytes a debt finds a new object's under the same key (`Superseded`) are left;
the migration's own copy of that object overwrites them, or cleanup, which
walks the new row, removes the source's.

## Addendum (2026-11-24): a promote is fenced against the rebind

Every writer resolves the collection's bucket, puts or finds the bytes there,
and then promotes the row — on another transaction. A rebind committed in
between made the row readable from the target while its bytes were only in
the source, and the cleanup then deleted the only copy. Ordering by
timestamps cannot close that: `bytes_written_at` was the promote
transaction's start, so a promote that committed late landed behind any
cursor or check that had already passed its time.

The promote is fenced instead (`statemachine.checkPlacement`):

- **It holds the migration row.** While the tenant's migration is in a state
  whose step moves a binding or stamps the copy's start
  (`migstate.FencingPromotes`: provisioning, copying, rebinding, verifying,
  rolling back), the promote takes the row `FOR SHARE` for its transaction.
  Promotes do not wait on each other; a step that takes the row `FOR UPDATE`
  — the copy's start, the rebind, the rebind back — waits for every promote
  in flight, and every later promote waits for it. Under `READ COMMITTED`
  each statement after the lock sees every promote that committed before.
  Outside those states nothing is locked.
- **It checks the binding.** The caller names the bucket it confirmed the
  bytes in (`statemachine.Placement`); under the fence the promote reads the
  collection's binding and refuses with `ErrBindingMoved` when it differs.
  `CompleteObject` and `CopyObject` answer `UNAVAILABLE` (a copy's bytes,
  under its own new key, are deleted and its row discarded); a multipart
  completion, the reconciler and a storage event leave the row `PENDING`,
  anchored to the bucket its URL names, for the next attempt to move.
- **It stamps the clock after the fence.** `bytes_written_at` is
  `clock_timestamp()` at the promote's `UPDATE`, after the lock: a step that
  held the fence first sees every later promote stamped after its own time.

Lock order is the object's row, then the migration's — the order the copy
and the purge already take — and the rebind takes no object row, so the
fence adds no cycle. The cost falls on promotes during a migration alone: one
more row lock, shared.

## Addendum (2026-11-24): a multipart completion follows its collection; cleanup checks the target

A multipart session assembles its object where its parts are, the bucket it
was anchored to at initiate. After a rebind that is the source:
`CompleteMultipartUpload` promoted the object there, the promote cleared the
anchor, and the cleanup — which waited only while the row was `PENDING` —
deleted the only copy.

- **The completion moves the object**, as `CompleteObject` moves a
  single-part upload (`multiparth.settle`): it looks the collection's binding
  up through the bucket gate as a write of bytes, copies the assembled object
  there under the same key, checks the copy's size against the assembled one
  and promotes it there, under the ETag the binding reports. The anchored
  copy is left for the cleanup. A promote the fence refuses — a rebind in
  between — follows the collection again, at most twice: the storage upload
  is consumed, so a client's retry could not assemble the parts again. A
  crash at any step leaves the row `PENDING` and anchored; the cleanup waits
  for it, and the reconciler moves and promotes it once the session's
  deadline passes. An object that breaks its registration is deleted from
  both buckets before its row is discarded.
- **The cleanup deletes a source copy only once the target holds it.** For
  each object it HEADs the target: at the recorded size, the source copy
  goes. Missing or at another size while the source holds it at the
  recorded size, the source copy is carried — copied to the target, checked,
  then deleted. Held as recorded by neither, it is kept and logged at Error,
  and the migration stays `completed` until a later pass can delete it. An
  object whose collection is not bound to the target keeps its source copy:
  it may be read from there. So the cleanup never deletes the only copy of a
  live object, whatever wrote it after the copy passed.

## Addendum (2026-11-24): writes behind the copy's cursor are caught up before the rebind

The copy walks the tenant's objects once, in `(collection, path)` order,
while the tenant keeps writing to the source. A write the cursor has passed
reached the source only: a new object is missing from the target, and the
verify rolled the migration back (for a busy tenant, every attempt); an
overwrite is stale there, and one of the same size passed the verify by size,
so the tenant then read the old bytes.

Two designs were open: route every write during the migration to both
buckets, or copy what was written behind the cursor again before the rebind.
Writing both buckets doubles every upload's bytes and every failure mode —
an upload half-written to one bucket, presigned URLs that can name only one
— for the length of the copy, and a write still needs a way to tell which
bucket's bytes are newer. The catch-up is chosen; it reuses the copy's hold
and the promote's fence, and moves only what changed.

- **The copy's start is fenced.** `copy_started_at` (migration 116) is read
  with `clock_timestamp()` under the migration row's lock, which every
  promote holds while the migration provisions. So every promote that took
  the fence committed before the copy listed anything, and is copied after
  its write; every later write is stamped after it. A promote that ran its
  fence before the migration's row existed holds nothing, and can commit
  across the copy's start stamped before it (BACKLOG).
- **The catch-up has no cursor.** It lists the live and trashed private
  objects whose `bytes_written_at` is at or after the copy's start and whose
  latest write it has not copied (`storage_migration_caught_up`, one row per
  object, cleared when an attempt starts its copy and when it completes). It
  copies each under its row's hold (`FOR KEY SHARE`, which a promote's
  `FOR UPDATE` waits on), so the write it records is the one whose bytes it
  copied, or an older one that leaves the object listed again; with the
  write it records the ETag the target reported for the copy. A set with no
  cursor cannot be outrun by a late commit: a promote stamped before a pass
  and committed after it is listed by the next. One batch a tick, in the
  `rebinding` state, logged with its counts.
- **The rebind refuses while anything is behind.** Under the migration row's
  lock — so every promote in flight has committed and every later one waits
  — the rebind counts the objects the catch-up would still list, and any is
  `ErrCopyBehind`: nothing changes, and the next tick copies them. A tenant
  that writes faster than the catch-up copies stays in `rebinding`; it is
  never rebound behind its writes.
- **The verify compares content and write time, not size alone.** For an
  object written between the copy's start and the rebind it requires the
  catch-up to have recorded that very write, and the target's ETag to be the
  one the target reported for that copy. ETags are compared with ETags the
  same bucket reported for the same copy, never across buckets: a
  server-side copy, a stream-through and an encrypted store each make their
  own. An object written before the copy started was copied after its
  write; one written at or after the rebind went to the target, its promote
  fenced against the rebind. A failure rolls back, as before.

Not covered, and recorded in BACKLOG: an overwrite reported only by a storage
event that arrives after the rebind is dropped before it reaches the row; the
rollback's own copy back still walks a cursor.

## Addendum (2026-11-24): a source write whose event comes after the rebind is moved

In an explicit-mode bucket an overwrite is recorded only when its storage
event is. One written to the source behind the copy's cursor and delivered
after the rebind named a bucket the collection was no longer bound to, and
was skipped: the target kept the older bytes, and the cleanup deleted the
newer ones with the source.

The other option was to hold the cleanup while such events can still arrive.
It cannot know when that is, and the target would still serve the older
bytes meanwhile; the write is moved instead, as `CompleteObject` moves an
upload.

- **The event is matched to the migration.** An uploaded event from a bucket
  the collection is not bound to — or one whose promote the fence refused
  with `ErrBindingMoved` — is a late source write when the tenant's
  migration rebound the collection from that bucket and still keeps its
  source (`migstate.ReboundToTarget`, `rebound_at` set). Any other is skipped
  as before.
- **It is moved under the object's row.** `PromoteLateSourceWriteInTx` holds
  the row (`FOR UPDATE`) and the migration's fence from the decision to the
  promote: it copies the source's bytes into the collection's bucket, checks
  the size, and promotes them there under the ETag that bucket reports.
- **Only when nothing newer is recorded.** The object must be `AVAILABLE`, the
  event's sequencer must follow the row's, and the row's last write must
  predate the rebind. A write recorded since went to the target, later than
  anything in the source; a sequencer cannot tell, since a bucket orders
  only its own events. Such an event is skipped and logged.

An event that arrives once the cleanup has deleted the source copy finds
nothing to move; it is logged at Error and skipped (BACKLOG). The rollback's
mirror — a target event delivered after the rebind back — is not moved
either (BACKLOG).

## Addendum (2026-11-24): a start waits for the promotes in flight

A promote holds the migration row only if the row is in a fencing state when
the promote reads it. One that read it before the migration existed, or
while it was `failed`, held nothing: it could commit after the copy listed
past its key, stamped before `copy_started_at`, and the catch-up skipped it.

Starting the copy only after a grace longer than any promote would rest on
a guess about how long a promote runs; a lock is chosen. The tenant's
row lock was not usable: the rebind updates `tenants` while holding the
migration row, and a promote holding the tenant row while waiting on the
migration row would close a cycle.

- **Every promote holds the tenant's start lock shared**
  (`pg_advisory_xact_lock_shared`, space `migstate.LockSpaceStart`, key
  `migstate.TenantLockKey`), in a statement before the one that reads the
  migration row. Promotes do not wait on each other.
- **A transition into a fencing state from a non-fencing one takes it
  exclusively**: a new start, a retry of a `failed` migration, and a stuck
  rollback. It waits for every promote in flight; a later promote waits for
  it, then reads the row it left and holds it. Taken only once no migration
  is active, so no promote holding the lock waits on a row a worker holds.

## Addendum (2026-11-24): a collection created during a rebind moves with the others

`CreateCollection` resolves its bucket — the tenant's default binding, most
often — before its transaction. One that resolved the source before the
rebind and inserted after it stayed in the source while the tenant was
`dedicated`; the mirror held for a rebind back. Rolling the migration back
when the verify finds such a collection was the alternative; a whole
migration is too much to pay for one creation.

- **The moves and the creation share a lock.** The rebind and the rebind
  back hold the tenant's bindings lock exclusively
  (`migstate.LockSpaceBindings`) before they list the collections; every
  private collection's creation holds it shared until it commits. A creation
  in flight is waited for and moved with the others.
- **A creation after the move re-checks its bucket.** Under the lock, a
  private collection bound to a bucket the tenant's latest migration moved
  away from — the source once rebound (`migstate.SourceLeft`), the target
  once rolled back — is refused with `ErrBucketMigratedAway` (`ABORTED`); a
  retry resolves the binding again. It is refused, not redirected: the
  caller's authorization and the resource name already named the bucket.

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
  migrated back to shared by the inverse of the Phase 3 copy job. Within a
  migration, the window in which that is automatic is between the rebind and
  a failed verify: the job rolls the tenant back to its source bucket itself
  (Phase 3 above). After `completed` the source copies are kept until
  `cleanup_after`; there is no automatic way back once they are cleaned. The
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
