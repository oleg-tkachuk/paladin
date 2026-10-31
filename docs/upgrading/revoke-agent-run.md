## Unreleased — revoke an agent run's capabilities

- `CapabilityService.RevokeRun` revokes every capability of one agent run in
  a tenant — the run's `subject.run_id` — and releases their holds at once.
  The caller's own tenant's run, or any tenant's for `platform.admin` and
  `platform.capability-issuer`; gated by `RevokeCapability`. A repeat revokes
  nothing.
- `CapabilityService.List` takes `run_id`, listing what `RevokeRun` would
  revoke.
- The console offers both on a capability that names its run.
