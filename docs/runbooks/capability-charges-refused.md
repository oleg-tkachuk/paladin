# Runbook: capability charges refused

Covers `PaladinCapabilityChargesRefused` in the backend chart's
PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.capabilityChargesRefused`).

| Fires when | Meaning |
|-----------|---------|
| over 90% of a tenant's capability charges are refused for budget, over 15m, for 15m | A tenant's calls under capabilities fail with `ResourceExhausted` nearly every time. |

With `capability.charge_per_request_amount` above zero, every billable data
call made under a capability (presigns, object reads and writes, batches) is
charged against two budgets: the capability's own, signed into it, and the
tenant's. An occasional refusal is a cap working. A tenant refused nearly
every time cannot work, and from outside it looks like a tenant that went
quiet.

## Signal source

`paladin_capability_charges_total{tenant_id, outcome}`
(`auth.ChargeCapability`, `internal/auth/capability_interceptor.go`):

| `outcome` | Refused by | Code to the client |
|-----------|-----------|--------------------|
| `charged` | — | — |
| `capability_exhausted` | the capability's budget, or a delegating ancestor's | `ResourceExhausted` |
| `tenant_exhausted` | the tenant's budget | `ResourceExhausted` |
| `error` | the charge itself failed | `Unavailable` |

The rule counts the two `*_exhausted` outcomes, which are
`metrics.alerts.rules.capabilityChargesRefused.refusedOutcomes`. `error`
only adds to the total. A capability over its `max_requests` caveat is
refused before any charge and does not appear here.

## Triage

1. **Which budget.** Split the tenant's refusals by outcome:

   ```
   sum by (outcome) (rate(paladin_capability_charges_total{tenant_id="<tenant>"}[15m]))
   ```

2. **`tenant_exhausted`: the tenant's budget is spent.** This is the tenant
   budget (`TenantBudgetService`), not storage quotas (`QuotaService`).
   Read it in the console (*Tenants → \<tenant\> → Budget*) or with
   `TenantBudgetService.Get` / `Summarize`: `max_budget_usd` against
   `spent_usd` and `reserved_usd`.

3. **`capability_exhausted`: a capability's budget is spent.** Find which
   one in the console (*Tenants → \<tenant\> → Capabilities*) or with
   `CapabilityService.List` and `GetUsage`. For a delegated capability the
   refusing budget may be an ancestor's; `GetUsage` on each in the chain
   shows which.

## Mitigation

- **Tenant budget.** If the spend is legitimate, raise it with
  `TenantBudgetService.Set`; with `ResetSpend` it also zeroes the spend, for
  a new billing period. If it is not, the refusals are the budget doing its
  job — find the client spending it.
- **Capability budget.** A capability's budget is signed into it and cannot
  be raised in place: issue a new capability with a larger
  `max_budget` and have the client switch to it.

## Escalation / notes

- `warning`: the refusals are by design, and nothing is lost; the client is
  told `ResourceExhausted`. Escalate to whoever owns the tenant's billing
  when the tenant's budget is the cause.
- A rise in `error` instead is a charging failure, not a budget: the client
  gets `Unavailable`. Look at the database, as for any write failing.
