## sdk/go/v0.137.0, sdk/python/v0.138.0 — managing capabilities spends no request

- A call of `CapabilityService` presented with a capability — `Delegate`,
  `Lease`, `GetLease`, `SettleLease`, `Get`, `List` and the rest — no longer
  counts against that capability's `max_requests`, nor its ancestors'. A
  capability at its request limit can still delegate, lease and settle; its
  limit bounds the work done under it. Every other caveat is still checked,
  and the tenant's rate limit still applies.
- The SDK fakes (`paladintest`, `paladin.testing`) count requests the same
  way.
