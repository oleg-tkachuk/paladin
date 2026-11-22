## Unreleased — a storage migration moves the trash with the tenant

- A shared→dedicated storage migration now copies, verifies and cleans up
  trashed objects as well as live ones, so `RestoreObject` works on an object
  trashed before the migration. It used to answer `FAILED_PRECONDITION` with
  `ERROR_REASON_OBJECT_BYTES_MISSING`, the bytes having stayed in the old
  bucket.
- `objects_total` and `objects_copied` on `GetTenantStorageMigration` count
  trashed objects too. A trashed object whose bytes were already gone from the
  bucket is counted as copied: there was nothing to move.
- A rollback copies back objects trashed after the rebind as well, so they
  stay restorable on the source bucket. They stay in the trash.
