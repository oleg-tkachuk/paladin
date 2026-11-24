## Unreleased — an operator can cancel a storage migration

- `CancelTenantStorageMigration` (admin `TenantService`) cancels a tenant's
  storage migration at the `resource_version` read from
  `GetTenantStorageMigration`. A `provisioning` migration fails at once; a
  `moving` or `completed` one rolls back — every object moves back to the
  shared bucket and new uploads go there again — and ends `failed`,
  retryable with `MigrateTenantStorageLayout`. The migration's `error`
  records who cancelled it. A migration `rolling_back`, `failed` or `cleaned`
  answers `FAILED_PRECONDITION`, a stale version `ABORTED`. Authorised as
  `MigrateTenantStorageLayout` is, and audited.
- `StorageMigrationStatus.resource_version` is new (migration 134): it moves
  with every change of state, never with a pass's progress.
- The console's storage migration card has a **Cancel migration** action,
  confirmed before it runs.
- The cleanup of a completed migration's source copies now deletes each one
  while holding the migration row, so a cancel waits for a delete in flight.
