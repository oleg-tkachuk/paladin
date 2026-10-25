## Unreleased — the API says what each capability operation unlocks

- **`CapabilityService.ListOperationGrants`** returns, for each operation a
  capability's caveats can name (`get`, `list`, `put`, `delete`, `manage`),
  the Cedar actions it unlocks in the capability's own tenant with no tenant
  policy. An operation with none (`manage`) needs a tenant policy naming its
  actions. Gated on `ReadCapability`. Additive.
- The console shows it when a capability is issued and on its record.
