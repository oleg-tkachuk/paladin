## Unreleased — Login refuses an upstream code

- `AuthService.Login` answers `INVALID_ARGUMENT` when `upstream_code` is set.
  It used to be accepted and ignored: no upstream IdP signs anyone in to
  Paladin. The field is deprecated in the proto; leave it unset.
- The console's login page no longer reads `?upstream_code=` from its URL.
