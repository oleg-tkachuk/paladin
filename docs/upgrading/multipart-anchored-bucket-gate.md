## Unreleased — multipart sessions answer the bucket gate of their own bucket

- `CompleteMultipartUpload` and `PresignPart` ask the bucket gate about the
  bucket the upload was started in, where its parts are, rather than the
  collection's current binding. After a storage migration rebinds the
  collection, an upload started on the old backend is refused while that
  backend drains (`FAILED_PRECONDITION`, `ERROR_REASON_BACKEND_READ_ONLY`),
  and no longer refused because the new binding is provisioning or draining.
- `AbortMultipartUpload` and `ListParts` now ask the gate too, as reads: a
  disabled backend refuses them (`FAILED_PRECONDITION`,
  `ERROR_REASON_BACKEND_DISABLED`). A draining, provisioning or failed bucket
  still takes them; aborting an upload frees storage.
- A part URL's lifetime is bounded by the `max_presign_put_ttl` of the bucket
  the upload was started in.
