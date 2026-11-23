## Unreleased — breaking: tenant policies can forbid capability delegation

- **`CapabilityService.Delegate` now asks Cedar `DelegateCapability`**, with
  the presented capability as the principal (`principal.kind ==
  "capability"`) and its own tenant as the resource, under
  `context.capability_op == "share"`. The built-in policy permits it in the
  capability's own tenant, so delegation works as before without a tenant
  policy. A tenant that wants to stop delegation can now `forbid` it, for
  example `forbid (principal, action == Action::"DelegateCapability", resource);`;
  `Delegate` then answers `PERMISSION_DENIED` ("denied by policy").
- **A forbid that already matches every capability now stops delegation
  too.** A tenant that followed the v40.2.0 note and forbids
  `principal.kind == "capability"` on every action could delegate until now;
  it cannot after this release. Narrow that `forbid` to the actions it means,
  or add `action != Action::"DelegateCapability"` to its condition.
- **`ListOperationGrants` reports `share` as `BUILTIN`** with the action
  `DelegateCapability`, instead of `CAVEATS_ONLY` with the `Delegate`
  procedure. No operation has the `CAVEATS_ONLY` basis any more; the enum
  value stays.
