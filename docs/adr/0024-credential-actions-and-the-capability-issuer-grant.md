# ADR-0024: Credential actions are named per resource, and the capability issuer is granted by the built-in policy

- **Status:** Accepted — implemented 2026-10-06 (`cedar.Action*Capability`,
  `cedar.Action*APIToken`, `cedar.ActionInspectMCP`, schema entries, built-in
  permit for `platform.capability-issuer`, tests on the real engine).
- **Related:** [ADR-0010](0010-capability-as-establishing-credential.md) (a
  capability establishes identity), [ADR-0011](0011-narrow-role-for-tenant-provisioning.md)
  (the same shape for tenant provisioning).

- **Context:** `platform.capability-issuer` exists so a consumer serving many
  tenants can mint a short-lived capability per tenant without holding
  `platform.admin`. The handlers admitted it across tenants and the roles list
  documented it, but no policy granted it anything: `CapabilityService` asked
  Cedar about `"issue"`, `"delegate"`, `"revoke"` and `"list"`, which no
  `Action*` constant, schema entry or policy named. Only the blanket
  `platform.admin` permit matched them, so every call the role made was denied
  before the handler's cross-tenant check ran. The API-token handler
  (`"api_token:create"` …) and the MCP inspection handler (`"read"`) had the
  same defect.

  It survived because the handler tests authorised through a stub that allows
  everything, and because the existing schema gate compares the schema with
  the `Action*` constants — a literal passed at the call site was never one of
  them.

- **Decision:**
  1. Every action these handlers authorise is a named constant declared in the
     schema, and named for its resource: `IssueCapability`,
     `DelegateCapability`, `RevokeCapability`, `ReadCapability`;
     `CreateAPIToken`, `RevokeAPIToken`, `ReadAPIToken`; `InspectMCP`. A grant
     to revoke or read one credential type does not carry over to the other.
  2. A built-in permit grants `platform.capability-issuer` exactly
     `IssueCapability`, `RevokeCapability` and `ReadCapability`. Built-in
     rather than per-tenant for the reason ADR-0011 gives: the role belongs to
     the platform, and a grant stored in one tenant's policy is lost on a
     re-seed. `DelegateCapability` is left out — the admin delegate path reads
     the parent under the caller's own tenant and could not reach what the
     issuer minted for others — and so is every API-token, tenant, IAM and
     data-plane action.
  3. Cross-tenant reach stays a handler decision (`capabilityh.spansTenants`).
     Cedar sees the caller's own tenant as the resource and answers only
     whether the role may perform the action at all.

- **Consequences:**
  - A consumer can issue per-tenant capabilities with a credential whose leak
    mints short-lived, caveated capabilities rather than one that deletes every
    tenant.
  - A tenant can still refuse it: first-forbid wins over the built-in permit.
  - The tests for these handlers run on the real engine, so an action no policy
    grants fails there rather than in a cluster.
