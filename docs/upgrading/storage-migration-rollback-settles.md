## Unreleased — a rolled-back storage migration keeps passing after it fails

- A storage migration that rolled back keeps running its passes while it is
  `failed`, until it settles: an upload or a collection that read the
  tenant's dedicated bucket before the switch back and was created after the
  rollback ended is moved back to the shared bucket. The passes stop once
  nothing of the tenant is left in the dedicated bucket and
  `worker.jobs.storage_migration.source_copy_retention` (by default
  `limits.presign.max_ttl`) has passed since the rollback ended. A
  regenerated or part URL for the dedicated bucket is bounded by the
  rollback's write deadline until then.
- The state stays `failed` and `MigrateTenantStorageLayout` retries it as
  before. A migration that failed while its bucket was provisioned is not
  passed.
- Migration 131 adds `tenant_storage_migrations.settle_after` and
  `settled_at`, and takes every migration already `failed` as settled.
