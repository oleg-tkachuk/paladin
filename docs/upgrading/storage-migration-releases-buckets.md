## Unreleased — a finished storage migration no longer holds its buckets

- A `failed` or `cleaned` storage migration no longer refuses the delete of
  its buckets: `DeleteBucket` on a migrated tenant's dedicated bucket, or on
  the shared bucket a tenant migrated off, goes through once nothing else
  holds it, and a migrated tenant can be purged. Migration 135 makes the
  migration's bucket references `ON DELETE SET NULL` and guards them with a
  trigger while the migration is `provisioning`, `moving`, `completed` or
  `rolling_back`; `DeleteBucket` then still answers `FAILED_PRECONDITION`,
  naming the storage migration.
- `PreviewPurgeTenant` reports a new blocker kind,
  `PURGE_BLOCKER_KIND_STORAGE_MIGRATION`, for a migration in flight, and
  `PurgeTenant` refuses it with `FAILED_PRECONDITION`: restore the tenant,
  cancel the migration or let it finish, then trash and purge it.
- A migration whose bucket was deleted reports that bucket as empty in
  `GetTenantStorageMigration`; a retry provisions the dedicated bucket anew.
