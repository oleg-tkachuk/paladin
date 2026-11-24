## Unreleased — the old storage-migration bookkeeping is dropped

- Migration 130 drops what the rebind-at-an-instant migration kept: the
  tables `storage_migration_caught_up`, `storage_migration_copied_back` and
  `storage_migration_source_etags`, and the columns `rebound_at`,
  `rebound_back_at`, `copy_started_at`, `rollback_cursor_at` and
  `rollback_cursor_id` of `tenant_storage_migrations` (ADR-0051). A binary
  from before stage 2 of ADR-0051 fails its migration queries once it runs;
  deploy this with the release that carries it.
- It also places every promoted object and anchors every pending upload
  that an older binary wrote without them.
- `MigrateTenantStorageLayout` on a `dedicated` tenant whose migration
  failed no longer starts a rollback: a failed migration now always leaves
  its tenant `shared`, and the call is refused as for any dedicated tenant.
