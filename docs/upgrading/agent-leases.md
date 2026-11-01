## Unreleased — an agent leases budget for what it delegated

- `CapabilityService.Lease`, `GetLease` and `SettleLease` accept a
  presented capability holding the `share` op, as `Delegate` does: it
  leases for itself or for any capability delegated from it, in its own
  tenant, with no Cedar check. A Biscuit copy is refused
  (`FAILED_PRECONDITION`); a capability not below it, or revoked, is
  `PERMISSION_DENIED`.
