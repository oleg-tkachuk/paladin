## Unreleased — writes that race a storage migration's rebind are not lost

- A completion, a copy or a storage event that promotes an object now
  checks, on the promote's own transaction, that the object's collection is
  still bound to the bucket its bytes were found in. When a storage
  migration rebound the collection in between, the promote is refused:
  `CompleteObject` and `CopyObject` answer `UNAVAILABLE` ("the collection
  moved to another bucket while the object was written; retry"), and a
  retry moves the upload into the new bucket. A batch copy item fails the
  same way and can be retried. A storage event the rebind overtook is
  skipped with a warning, and the reconciler settles its upload.
- A multipart upload initiated before a storage migration's rebind and
  completed after it is moved into the collection's new bucket before it is
  promoted; it was lost at the migration's cleanup. Its `etag` is the one the
  new bucket reports for the moved copy. A move that fails answers
  `UNAVAILABLE`; the object stays pending and the reconciler settles it once
  the session's deadline passes.
- A migration's cleanup deletes an object's copy in the source bucket only
  once the target holds it at its recorded size, carrying it to the target
  first when only the source does. A copy neither bucket holds as recorded
  is kept and logged at Error, and the migration stays `completed` until it
  is resolved.
- Before a migration rebinds a tenant it copies again every object written
  since its copy started (migration 116 adds
  `tenant_storage_migrations.copy_started_at` and the
  `storage_migration_caught_up` table; 117 builds an index on
  `objects (tenant_id, bytes_written_at)` concurrently). The migration stays
  in `rebinding` while it catches up, logging each pass with its counts, and
  a rebind refused by writes since the last pass is logged at Warn. Objects
  uploaded or overwritten behind the copy's cursor no longer roll the
  migration back or leave the tenant reading older bytes. The verify also
  checks such an object's write time and its ETag against the catch-up's
  copy, not its size alone. A migration already past its copy when this
  ships has no `copy_started_at` and counts from its creation: one in
  `rebinding` catches up on every write since, and one in `verifying` rolls
  back if anything was written during its copy, and can be retried.
- While a migration is provisioning, copying, rebinding, verifying or
  rolling back, every promote of one of the tenant's objects holds the
  migration's row for its transaction (`FOR SHARE`). Promotes do not wait on
  each other; the migration's rebind waits for the promotes in flight.
