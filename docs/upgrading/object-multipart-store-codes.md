## Unreleased — object and multipart store failures are no longer the caller's fault

- `CompleteMultipartUpload`, `AbortMultipartUpload`, `PresignPart` and
  `ListParts` answered `NOT_FOUND` whenever the upload session could not be
  read, an unavailable database included. Only an upload that is not there is
  `NOT_FOUND` now; a store failure is `INTERNAL` and worth retrying.
- `PresignPart` answers a presigner failure with `INTERNAL`; it had no code.
- `LookupObject` answers a store failure with `INTERNAL`, not `NOT_FOUND`.
- `CompleteObject` answers a storage backend it could not reach with
  `UNAVAILABLE`; `FAILED_PRECONDITION` is kept for nothing uploaded yet.
