## v41.1.0, sdk/go/v0.126.1, sdk/python/v0.127.1 — limes v1.0.0-rc.14: a capability no request can use is refused

- **`max_requests` is at least 1.** `CapabilityService.Issue` and
  `Delegate` refuse `max_requests: 0` as `INVALID_ARGUMENT`: a capability
  that may make no request can do nothing, and limes no longer issues one.
  To stop a capability, revoke it; to bound it, set a limit of 1 or more, or
  `unlimited_requests`. Capabilities issued before the upgrade keep their
  ceilings. The console's issue dialog asks for at least 1.
- A Biscuit copy's own `max_requests` may still be 0.
