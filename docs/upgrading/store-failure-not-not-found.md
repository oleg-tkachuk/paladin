## Unreleased — a store failing behind a lookup is INTERNAL, not NOT_FOUND

- `GetTenant`, `PurgeTenant`, `PreviewPurgeTenant`, `GetTenantStorageMigration`
  and `MigrateTenantStorageLayout` answer `NOT_FOUND` only for a tenant that is
  not there, with the message `tenant not found`; `GetTenantStorageMigration`
  for a tenant with no migration says `no storage migration for this tenant`,
  still `NOT_FOUND`. A database
  that could not be read is `INTERNAL`, with a request id; it used to be
  `NOT_FOUND`, carrying the driver's error as its message.
- `MigrateTenantStorageLayout` answers `FAILED_PRECONDITION` for a tenant with
  no private collections, or with them in more than one bucket, as before; a
  database that could not say which bucket is `INTERNAL`, not
  `FAILED_PRECONDITION`.
- The same holds for the IAM calls that load a user first: `WhoAmI`,
  `ListMyMemberships`, `SwitchTenant`, `ChangePassword`, and the admin user
  calls (`UpdateUser`, `DeleteUser`, `GrantScopes`, `RevokeScopes`,
  `ResetPassword`, …). A user that is not there is `NOT_FOUND` with the message
  `user not found` (it was `auth/store: not found`); a store that failed is
  `INTERNAL`.
