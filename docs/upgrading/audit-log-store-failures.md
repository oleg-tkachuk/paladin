## Unreleased — ListAuditLog answers a failing store as INTERNAL

- `ListAuditLog` answers `INTERNAL` when the store fails; it used to answer
  `UNKNOWN`. A page token the store cannot resume is still
  `INVALID_ARGUMENT`.
