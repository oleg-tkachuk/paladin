# ADR-0032: Every bucket has a tenant owner, and sharing is granted

- **Status:** Accepted — implemented 2026-10-16 (`080_bucket_owner_and_grants.sql`,
  `bucketh/grants.go`, `BucketService.GrantBucket` / `RevokeBucketGrant` /
  `ListBucketGrants`).
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (shared and
  dedicated layouts; this replaces its rule that a bucket with no owner is
  open to every tenant), [ADR-0025](0025-bucket-quotas-are-platform-configuration.md)
  (bucket quotas), [ADR-0027](0027-public-collections.md) (public buckets),
  [ADR-0028](0028-bucket-ownership.md) (whether Paladin manages a bucket on
  its backend — the other half of ownership).

- **Context.** `buckets.owner_tenant_id` is nullable, and NULL means *shared*:
  the collection-binding trigger (`collections_enforce_bucket_tenancy`, 003)
  lets any tenant bind a collection to such a bucket. Every bucket on the dev
  cluster ended up NULL, by three routes:
  - `EnsureTenantStorage`, the data-plane self-service, creates its bucket
    with no owner by design, under a name the tenant chooses.
  - `CreateBucket` accepts an empty `owner_tenant_id`; a consumer that serves
    several of its own tenants from one bucket used it, and so did the e2e
    fixtures (`paladin-e2e`).
  - Until #520 an owner given by slug was dropped without a word.

  So "shared" means "open to all", not "shared with chosen tenants". A
  platform admin's test bound a collection into one consumer's bucket and
  nothing objected. Any tenant holding `manage` can call `EnsureTenantStorage`
  naming another consumer's bucket — a public one included — and get a
  collection there.
  Its keys stay under its own tenant prefix, so it reads nothing of the
  other's, but everything set on the bucket applies to it: quotas, lifecycle
  rules, the bucket policy, public read. A bucket's events have no tenant to
  go to either. Sharing a bucket is still worth having — each bucket costs
  volume slots on SeaweedFS, and ADR-0015's shared layout rests on it — but
  it has to be a decision someone made.

- **Decision.**
  1. **Every bucket has an owner.** `buckets.owner_tenant_id` becomes
     `NOT NULL`; `owner_tenant_id` on `CreateBucket` becomes required and an
     empty one is `INVALID_ARGUMENT`. A pool the operator runs for many
     tenants belongs to the platform tenant. A bucket's events go to its
     owner.
  2. **Sharing is a grant.** A new table, keyed on the bucket's id — which
     already fixes its backend, as every table that references a bucket
     does:

     ```sql
     CREATE TABLE bucket_grants (
         bucket_id  uuid NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
         tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
         granted_by text NOT NULL,
         granted_at timestamptz NOT NULL DEFAULT now(),
         PRIMARY KEY (bucket_id, tenant_id)
     );
     CREATE INDEX bucket_grants_tenant_idx ON bucket_grants (tenant_id);
     ```

     The binding trigger admits a collection whose tenant owns the bucket or
     holds a grant on it, and nothing else; "NULL admits anyone" goes.
  3. **Grants are admin-plane.** `BucketService.GrantBucket` and
     `RevokeBucketGrant`, under the owner's `ManageBucket`.
     `platform.tenant-provisioner` may grant, since placing a tenant it
     provisions on a pool is the decision it already makes with a default
     binding; revoking stays with the bucket-admin roles. A grant is not
     revoked while the tenant still has collections in the bucket
     (`FAILED_PRECONDITION`), as a collection with objects is not deleted. The
     console's bucket page names the owner and lists the grants.
  4. **Self-service creates for itself.** `EnsureTenantStorage` creates a
     missing bucket owned by the calling tenant. It binds into an existing
     bucket only when that tenant owns it or holds a grant, and answers
     `PERMISSION_DENIED` otherwise — never a silent binding. ADR-0028's rule
     that self-service never adopts an unregistered bucket stands.
  5. **A default binding needs the right to bind.** Setting a tenant's
     default binding (`tenant_default_bindings`) to a bucket it neither owns
     nor holds a grant on is refused; the shared layout is a grant on the
     operator's pool, made when the binding is set.
  6. **Migration of the NULL rows**, in the migration that adds the
     constraint:
     - collections from exactly one tenant: that tenant owns the bucket;
     - collections from several: the platform tenant owns it, and each of
       them gets a grant;
     - no collections: the platform tenant owns it.

     A collection created in a bucket by mistake should be removed first, or
     it turns a single-tenant bucket into a platform-owned shared one.

- **Consequences.** A breaking change to the API: `owner_tenant_id` required,
  two new RPCs, `EnsureTenantStorage` refusing what it used to bind. A major
  release and a new contract baseline. Consumers move with it: one that
  provisions through `EnsureTenantStorage` needs nothing, since it becomes the
  owner of its bucket; one that creates a bucket for several of its tenants
  names an owner and grants each tenant it provisions; the e2e fixtures grant a test tenant on `paladin-e2e` and revoke
  it on teardown. Every bucket has one tenant answerable for it, and a tenant
  sharing a bucket is there because the owner let it in.

- **Alternatives considered.**
  - *Forbid sharing: one bucket per tenant.* Simplest, and what the dedicated
    layout already is, but it multiplies buckets — and volume slots on
    SeaweedFS — by the tenant count, and drops a layout ADR-0015 keeps on
    purpose.
  - *Keep NULL as "shared" and only stop self-service from creating it.* Closes
    one of the three routes; a NULL bucket stays open to every tenant, and a
    bucket's events stay ownerless.
  - *Carry the backend in `bucket_grants` too.* `bucket_id` already names the
    backend through `buckets`; a second copy could disagree with it unless a
    composite foreign key held them together, for a value always derivable.
