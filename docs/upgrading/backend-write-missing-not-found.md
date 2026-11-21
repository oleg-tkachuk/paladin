## Unreleased — updating or deleting a backend that is not there is NOT_FOUND

- `UpdateBackend` and `DeleteBackend` on a storage backend that is not
  registered now answer `NOT_FOUND`, as `GetBackend` does. They used to answer
  `ABORTED` with `ERROR_REASON_VERSION_CONFLICT`, which told a client to re-read
  and retry. `ABORTED` is still the answer for a stale `resource_version`.
