## Unreleased — writing a user that is not there is NOT_FOUND

- `UpdateUser` and `DeleteUser` on a user removed after it was read now
  answer `NOT_FOUND` with `ERROR_REASON_NOT_FOUND`. They used to answer
  `ABORTED` with `ERROR_REASON_VERSION_CONFLICT`. `ABORTED` is still the
  answer for a stale `resource_version`.
