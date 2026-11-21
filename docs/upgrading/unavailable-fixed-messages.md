## Unreleased — `UNAVAILABLE` answers no longer quote the backend that failed

The code is unchanged; the message is a fixed sentence and the backend's own
error is in the server's RPC log line.

- Completing an upload, or restoring an object, when the storage backend could
  not be asked: `the uploaded bytes could not be checked; retry` and `the
  object's bytes could not be checked; retry`, instead of the S3 client's
  error with its URL and request id.
- Creating or adopting a bucket when the backend could not say whether it
  exists: `the backend could not be asked whether the bucket exists; retry`.
- `PlatformStatsTenants` when the worker did not answer: `the worker's census
  could not be read; retry`.
- A capability whose taint lookup, or whose delegating user's lookup, failed:
  `the capability could not be checked; retry`, as other capability outages
  already read.
