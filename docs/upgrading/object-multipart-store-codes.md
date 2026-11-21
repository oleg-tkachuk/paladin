## Unreleased — multipart store failures are INTERNAL, not NOT_FOUND

- `CompleteMultipartUpload`, `AbortMultipartUpload`, `PresignPart` and
  `ListParts` answered `NOT_FOUND` whenever the upload session could not be
  read, an unavailable database included. Only an upload that is not there is
  `NOT_FOUND` now; a store failure is `INTERNAL` and worth retrying.
- `PresignPart` answers a presigner failure with `INTERNAL`; it had no code.
