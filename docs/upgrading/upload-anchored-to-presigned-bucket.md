## Unreleased — an upload presigned before a storage migration's rebind completes

- An upload records the bucket its URL is signed for. When a storage
  migration rebinds the collection between `UploadObject` and the PUT, the
  bytes land in that bucket, and `CompleteObject` now finds them there and
  moves them into the collection's new bucket before promoting the object.
  It used to answer `FAILED_PRECONDITION` ("object not uploaded yet"), and the
  object was discarded once its URL expired. The reconciler settles an
  upload left without a `CompleteObject` the same way.
- The object's `etag` is the one the collection's bucket reports for the
  moved copy, which can differ from the ETag the PUT returned. A
  `CompleteObject` that passes the PUT's ETag is still accepted.
- A disabled backend behind the bucket the URL named refuses the completion
  (`FAILED_PRECONDITION`, `ERROR_REASON_BACKEND_DISABLED`); a failed move
  answers `UNAVAILABLE`, and a retry repeats it.
- A migration's cleanup of the source bucket waits while an upload whose URL
  names it is still pending, at most until its URL expires and the
  reconciler settles it.
