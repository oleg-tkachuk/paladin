## Unreleased — a default binding for a missing tenant answers NOT_FOUND

- `SetTenantDefaultBinding` naming a tenant id that does not exist answered
  `PERMISSION_DENIED` (`ERROR_REASON_BUCKET_NOT_GRANTED`), or
  `INVALID_ARGUMENT` when the bucket was missing too. It now answers
  `NOT_FOUND` for the tenant, as the slug form of the name already did.
