## Unreleased — limes v1.0.0-rc.31

- The limes schema moves to version 36. pgstore v0.60.0 migrates it on
  start, and keeps a token exchange's record until its subject token
  expires.
- New limes codes for budget leases reach the wire: `lease_exhausted` and
  `lease_requests_exhausted` are `RESOURCE_EXHAUSTED`; `lease_not_found`,
  `lease_nested` and `leasing_disabled` are `FAILED_PRECONDITION`.
- **`capability.revocation_cache_max_stale`** (default `30s`, at most
  `1h`): while the revocation store fails, a cached answer stands in for
  this long past its TTL instead of refusing every capability. Each stale
  answer is logged at WARN and counted by `limes.revocation_cache.stale`.
  A revocation made during the outage goes unseen for as long; set `0` to
  fail closed as before.
- **Budget leases.** `CapabilityService.Lease` holds budget and requests
  of a capability's chain for it in advance, so its charges stop queueing
  on every ancestor and the tenant; `GetLease` reads it and `SettleLease`
  ends it, charging what was spent. Lease and SettleLease are gated as
  `Issue`, GetLease as reading a capability. An expired lease is settled
  by the capability purger, a revoked capability's by the revoke.
- **`capability.budget_leases`** (default `true`) lets `Lease` open
  leases. Turn it off while any replica still runs a release before this
  one; `Lease` is then refused with limes code `leasing_disabled`.
