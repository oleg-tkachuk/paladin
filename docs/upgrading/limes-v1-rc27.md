## Unreleased — limes v1.0.0-rc.27

- `CapabilityService.Revoke` releases the revoked capability's open holds,
  and those of the capabilities delegated from it, at once. Before, they
  counted against the tenant's budget until they expired. A revoke retried
  after a failed release finishes it.
- pgx stays on 5.11: pgx 5.12 changes an interface pgstore v0.54.0
  implements, and the backend does not build against it.
