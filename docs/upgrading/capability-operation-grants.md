## sdk/go/v0.122.0, sdk/python/v0.123.0 — the API says what each capability operation unlocks

- **`CapabilityService.ListOperationGrants`** returns a row for each
  operation a capability's caveats can name, with its `basis`
  (`CapabilityGrantBasis`): `BUILTIN` with the Cedar `actions` the built-in
  policy grants in the capability's own tenant; `TENANT_POLICY` for `manage`,
  which needs a tenant policy; `PAIRED` for `object:presign`, asserted beside
  the `paired_ops` that grant the action; `CAVEATS_ONLY` for `share`, whose
  `procedures` (`Delegate`) ask no policy. Gated on `ReadCapability`.
  Additive.
- The console shows it when a capability is issued and on its record.
