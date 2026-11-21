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
