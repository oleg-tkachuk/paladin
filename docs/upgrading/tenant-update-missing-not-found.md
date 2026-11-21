## Unreleased — updating a tenant that is not there is NOT_FOUND

- `UpdateTenant` on a tenant that does not exist now answers `NOT_FOUND` with
  `ERROR_REASON_NOT_FOUND`, as `GetTenant` does. It used to answer
  `ABORTED` with `ERROR_REASON_VERSION_CONFLICT`, which told a client to
  re-read and retry. `ABORTED` is still the answer for a stale
  `resource_version`, and a tenant in the trash still answers
  `FAILED_PRECONDITION`.
