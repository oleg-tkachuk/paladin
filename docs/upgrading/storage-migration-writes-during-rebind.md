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
- While a migration is provisioning, copying, rebinding, verifying or
  rolling back, every promote of one of the tenant's objects holds the
  migration's row for its transaction (`FOR SHARE`). Promotes do not wait on
  each other; the migration's rebind waits for the promotes in flight.
