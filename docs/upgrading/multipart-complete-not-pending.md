## Unreleased — completing a multipart upload whose object moved on is refused

- `CompleteMultipartUpload` for an upload whose object is no longer PENDING
  went on to assemble the parts. It now answers `FAILED_PRECONDITION`
  unless the object is AVAILABLE with this completion's checksum: a retry
  of a completion whose object was already promoted is not refused.
