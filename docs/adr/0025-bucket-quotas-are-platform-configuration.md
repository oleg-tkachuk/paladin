# ADR-0025: Bucket quotas are platform configuration, outside RLS

- **Status:** Accepted — implemented 2026-10-06 (`044_bucket_quotas.sql`,
  `bucket_quotas`, the promote path charging the bucket, tests on a
  NOBYPASSRLS pool).
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (bucket
  ownership), [ADR-0022](0022-data-plane-acts-on-the-named-tenant.md) (uploads
  count against the target tenant's quotas).

- **Context:** a quota was one `quotas` row scoped to a tenant (`tenant_id`)
  or to a bucket (`bucket_id`, `tenant_id` NULL). `quotas` carries the tenant
  isolation policy, which admits a row only when `tenant_id` equals the
  session's tenant, and NULL equals nothing. So through the runtime pool a
  bucket row could not be written (WITH CHECK), read (GetQuota answered
  NotFound) or seen by the upload check: no bucket cap was ever enforced.
  The worker reads on a BYPASSRLS pool, so the reconciler and the census
  showed the rows as healthy, and the component tests ran as a superuser, for
  whom RLS never engages. Separately, the promote path charged only the
  tenant's quota, so a bucket's daily counters never moved at all.

- **Decision:**
  1. Bucket caps live in `bucket_quotas`, one row per bucket, deliberately
     without RLS — the same class as `buckets` and `storage_backends`. A
     bucket's cap is not any one tenant's data: on a shared bucket it limits
     every tenant at once.
  2. `quotas` holds tenant caps only; `tenant_id` is `NOT NULL`.
  3. Writes stay gated where they were: the quota handler's role check
     (`platform.admin` or `bucket.admin`) and Cedar.
  4. Promoting an object charges the tenant's quota and the quota of the
     bucket its collection is bound to — the same object → collection →
     bucket path the reconciler sums over.
  5. A bucket quota's `paladin.quota.set` goes to the tenant owning the
     bucket; a shared bucket has no owner and notifies nobody.

- **Consequences:**
  - Anyone allowed to read quotas can read a shared bucket's aggregate usage
    — the bytes and object count of every tenant on it, summed. That is the
    price of the cap being enforceable for every uploader; it discloses no
    per-tenant figure.
  - The two tables share one id space (the migration kept each moved row's
    id), so `ResetQuotaUsage` resolves a quota id against either.
  - Tests for quota behaviour run on `rlsPool`; a superuser pool cannot see
    this class of defect.

- **Alternatives considered:**
  - *A policy admitting NULL `tenant_id` rows in `quotas`.* Keeps one table,
    but makes the isolation policy carry an exception every reader must know
    about, and leaves "tenant or bucket" as a nullable-pair invariant.
  - *Scope bucket rows to `buckets.owner_tenant_id`.* Works for dedicated
    buckets only; a shared bucket — the case a bucket cap exists for — has no
    owner and would stay invisible.
