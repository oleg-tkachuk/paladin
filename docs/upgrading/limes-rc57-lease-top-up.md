## sdk/python/v0.145.0, sdk/go/v0.145.0 — a lease top-up opens a new lease

- `CapabilityService.Lease` on a capability whose lease is open now settles
  that lease — what it spent charged to every ancestor and the tenant — and
  opens a new one under a new id, holding what the old one had left plus the
  request, until the request's TTL from now. It added to the open lease and
  kept its id. A caller that kept a lease's id across a top-up reads it from
  the top-up's answer. Both SDKs' fakes do the same.
- Delegating from a capability whose chain is at the depth limit answers
  `FAILED_PRECONDITION` with limes code `delegation_depth_exceeded`, where it
  named `delegation_too_deep`. Issuing for a tenant whose usage was erased
  answers `FAILED_PRECONDITION` with `tenant_gone`, where it answered
  `UNAUTHENTICATED` with `tenant_erased`; delegating there keeps
  `FAILED_PRECONDITION`, now with `tenant_gone`.
- A capability the revocation store has no record of is refused, where it
  was accepted as not revoked; every capability Paladin issues is recorded.
