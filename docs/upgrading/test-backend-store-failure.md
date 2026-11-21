## Unreleased — TestBackend answers a store failure as INTERNAL

- `TestBackend` answers `INTERNAL` when the backend cannot be read; it
  answered `NOT_FOUND` for any failure, so a database outage read as a
  backend that does not exist. A backend that is not registered is still
  `NOT_FOUND`.
