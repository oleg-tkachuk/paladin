## Unreleased — TenantBudgetService authorizes each call

`TenantBudgetService` now asks the policy engine on every call, under two new
Cedar actions on the `Tenant` resource: `ReadTenantBudget` and
`ManageTenantBudget`.

| RPC | Allowed to |
|-----|------------|
| `Get`, `ListReservations` | `platform.admin` for any tenant; `tenant.admin` for its own tenant |
| `Set` | `platform.admin` |
| `Summarize` | `platform.admin` |

- Any other caller is `PERMISSION_DENIED`, and a call with no principal is
  `UNAUTHENTICATED`. `bucket.admin`, `iam.admin`,
  `platform.tenant-provisioner` and `platform.capability-issuer` hold no
  budget grant.
- The tenant admin's read is a built-in permit, so it reaches every tenant
  whatever policy it was created with. A tenant policy can still `forbid`
  `ReadTenantBudget`.
- `Set` and `Summarize` need the `platform.admin` role in addition to the
  Cedar decision: the budget is a spend ceiling the platform sets, and
  `Summarize` reads every tenant's.
- A caller the gate refuses is answered before the capability subsystem's
  state, so `UNAVAILABLE` for a disabled subsystem now reaches only callers
  allowed the call.
- The console shows a tenant admin its tenant's budget and holds without the
  form to change them, and leaves the dashboard's budget alerts to platform
  admins.
