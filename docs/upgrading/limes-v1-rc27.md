## Unreleased — limes v1.0.0-rc.27

- `CapabilityService.Revoke` releases the revoked capability's open holds,
  and those of the capabilities delegated from it, at once. Before, they
  counted against the tenant's budget until they expired. A revoke retried
  after a failed release finishes it.
- pgx is on 5.12, with pgstore v0.54.1, which implements the interface pgx
  5.12 widened.
