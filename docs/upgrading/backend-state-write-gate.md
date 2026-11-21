## Unreleased — every write answers the backend's and bucket's state alike

- A bucket still provisioning on its backend now refuses a write with
  `UNAVAILABLE` and the new reason `ERROR_REASON_BUCKET_PROVISIONING`, not
  `FAILED_PRECONDITION`: the reconciler creates the bucket on its own, so a
  retry helps. A bucket that failed to provision, or is being deleted, refuses
  a write with `FAILED_PRECONDITION` (`storage bucket is not ready for
  writes`).
- A draining (read-only) or disabled backend's refusal now carries its
  reason, `ERROR_REASON_BACKEND_READ_ONLY` or `ERROR_REASON_BACKEND_DISABLED`,
  in the error's `ErrorInfo`. It came with the code alone before.
- `RestoreObject`, `RestoreVersion`, `UpdateObject` (and the tag RPCs on it),
  `DeleteObject` to the trash, `SetObjectTaint` and `CompleteObject` — its
  already-completed no-op included — used to proceed on a draining or
  provisioning backend. They are now refused there like every other write.
- A write whose bucket could not be looked up is refused with `INTERNAL`
  rather than authorized without its bucket and carried out.
- `GetObjectLock` is a read: a draining backend now serves it instead of
  refusing it with `FAILED_PRECONDITION`.
- `CompleteMultipartUpload` and part URLs (`PresignPart`) for a session opened
  before its backend began draining used to go through; they are now refused
  like every other write. `AbortMultipartUpload` is unchanged.
- The batch delete, tag, restore and copy submits answer the same states the
  single-object writes do, before anything is queued; a copy may still read
  from a draining source.
