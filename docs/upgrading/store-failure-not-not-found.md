## Unreleased — a store failing behind a lookup is INTERNAL, not NOT_FOUND

- `GetTenant`, `PurgeTenant`, `PreviewPurgeTenant`, `GetTenantStorageMigration`
  and `MigrateTenantStorageLayout` answer `NOT_FOUND` only for a tenant (or a
  migration) that is not there, with the message `tenant not found`. A database
  that could not be read is `INTERNAL`, with a request id; it used to be
  `NOT_FOUND`, carrying the driver's error as its message.
