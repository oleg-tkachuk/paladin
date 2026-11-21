## Unreleased — writing to a collection that is not there is NOT_FOUND

- `UpdateCollection`, `DeleteCollection` and `BindCollectionToBucket` on a
  collection the tenant does not have now answer `NOT_FOUND`, as
  `GetCollection` does. They used to answer `ABORTED` with
  `ERROR_REASON_VERSION_CONFLICT`, which told a client to re-read and retry.
  `ABORTED` is still the answer for a stale `resource_version`.
