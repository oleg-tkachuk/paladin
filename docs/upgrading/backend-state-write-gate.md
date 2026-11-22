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
- `RestoreObjectVersion` and `CompleteObject` — its already-completed no-op
  included — used to proceed on a draining or provisioning backend. They are
  now refused there like every other write of bytes.
- A write that changes only Paladin's rows and leaves the backend's bytes as
  they are goes through on a draining backend and on a bucket that is
  provisioning, failed or being deleted; a disabled backend refuses it with
  `ERROR_REASON_BACKEND_DISABLED`. These are `UpdateObject` (and the tag RPCs
  on it), `DeleteObject` to the trash, `RestoreObject`,
  `SetObjectRetention` and `SetObjectLegalHold`. The last two used to be
  refused while the backend drained.
- `SetObjectTaint`, setting or clearing, is answered by no backend or bucket
  state: it goes through on a draining, disabled or provisioning backend,
  decided by Cedar alone. A taint keeps content from capabilities, so it must
  be settable while the backend still serves that content. It is still
  refused with `INTERNAL` when the bucket cannot be looked up.
- A write whose bucket could not be looked up is refused with `INTERNAL`
  rather than authorized without its bucket and carried out.
- `GetObjectLock` is a read: a draining backend now serves it instead of
  refusing it with `FAILED_PRECONDITION`.
- `CompleteMultipartUpload` and part URLs (`PresignPart`) for a session opened
  before its backend began draining used to go through; they are now refused
  like every other write. `AbortMultipartUpload` is unchanged.
- The batch delete, tag, restore and copy submits answer the same states the
  single-object writes do, before anything is queued: a batch delete to the
  trash, a tag batch and a restore batch go through on a draining backend, a
  permanent batch delete does not, and a copy may still read from a draining
  source.
