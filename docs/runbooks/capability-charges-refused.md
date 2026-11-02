# Runbook: capability charges refused

Covers `PaladinCapabilityChargesRefused` in the backend chart's
PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.capabilityChargesRefused`).

| Fires when | Meaning |
|-----------|---------|
| over 90% of a tenant's metered calls under capabilities are refused by a budget or a request limit, over 15m, for 15m | A tenant's calls under capabilities fail with `ResourceExhausted` nearly every time. |

With `capability.charge_per_request_amount` above zero, every billable data
call made under a capability (presigns, object reads and writes, batches) is
charged against two budgets: the capability's own, signed into it, and the
tenant's. A capability with a request limit is also counted per call, as a
request bump. An occasional refusal is a cap working. A tenant refused nearly
every time cannot work, and from outside it looks like a tenant that went
quiet.

## Signal source

`limes_charge_decisions_total{limes_tenant_id, limes_outcome}` and
`limes_request_bumps_total{limes_tenant_id, limes_outcome}`, limes's own
counters of every charge and every request bump (its otelmetric package,
wired around the usage store in `internal/app/build_capability.go`). A
bump's outcome is `allowed`, `request_limit_exceeded` or `error`:

| `limes_outcome` | Refused by | Code to the client |
|-----------------|-----------|--------------------|
| `allowed` | — | — |
| `overrun` | — (committed past a ceiling under a record-overrun policy) | — |
| `budget_exceeded` | the capability's budget, a delegating ancestor's, a Biscuit copy's or a lease's | `ResourceExhausted` |
| `tenant_budget_exceeded` | the tenant's budget | `ResourceExhausted` |
| `request_limit_exceeded` | a request limit, a copy quota or a lease's requests | `ResourceExhausted` |
| `error` | anything else: a charge in another currency, a failed charge | `FailedPrecondition` for another currency, a capability no longer on record or a counter overflow; `Unavailable` or `InvalidArgument` otherwise |

The rule counts `budget_exceeded`, `tenant_budget_exceeded` and
`request_limit_exceeded` over both counters, which are
`metrics.alerts.rules.capabilityChargesRefused.refusedOutcomes`. The other
outcomes only add to the total. limes counts a charge in another currency as
`error`, so the rule no longer fires on it; the client's
`FailedPrecondition` is the signal.

## Triage

1. **Which budget.** Split the tenant's refusals by outcome:

   ```
   sum by (__name__, limes_outcome) (rate({__name__=~"limes_charge_decisions_total|limes_request_bumps_total", limes_tenant_id="<tenant>"}[15m]))
   ```

2. **`tenant_budget_exceeded`: the tenant's budget is spent.** This is the tenant
   budget (`TenantBudgetService`), not storage quotas (`QuotaService`).
   Read it in the console (*Tenants → \<tenant\> → Budget*) or with
   `TenantBudgetService.Get` / `Summarize`: `max_budget` against `spent`.
   A `max_budget` of zero refuses every charge; `unlimited_budget` refuses
   none.

3. **`error` with `FailedPrecondition`: the charge is in another currency.** A capability's
   counter, an ancestor's or the tenant's budget already counts in one unit,
   and the charge — always in the capability's own unit — names another.
   Every retry fails the same way. Issue the capability in the tenant's unit,
   or reset the tenant budget's spend with `TenantBudgetService.Set` before
   moving it to the new one.

4. **`budget_exceeded`: a capability's budget is spent.** Find which
   one in the console (*Tenants → \<tenant\> → Capabilities*) or with
   `CapabilityService.List` and `GetUsage`. For a delegated capability the
   refusing budget may be an ancestor's; `GetUsage` on each in the chain
   shows which.

5. **`request_limit_exceeded`: a request limit is spent.** The capability's
   `max_requests`, an ancestor's, a Biscuit copy's quota or a lease's
   requests. `CapabilityService.GetUsage` shows the requests counted against
   each capability in the chain; `GetLease` a lease's.

## Mitigation

- **Tenant budget.** If the spend is legitimate, raise it with
  `TenantBudgetService.Set`; with `ResetSpend` it also zeroes the spend, for
  a new billing period. If it is not, the refusals are the budget doing its
  job — find the client spending it.
- **Capability budget.** A capability's budget is signed into it and cannot
  be raised in place: issue a new capability with a larger
  `max_budget` and have the client switch to it.
- **Request limit.** Signed in like the budget: issue a new capability with
  a larger `max_requests`. A lease's requests are topped up with
  `CapabilityService.Lease`.

## Escalation / notes

- `warning`: the refusals are by design, and nothing is lost; the client is
  told `ResourceExhausted`. Escalate to whoever owns the tenant's billing
  when the tenant's budget is the cause.
- A rise in `error` instead is a charging failure, not a budget: the client
  gets `Unavailable`. Look at the database, as for any write failing.
