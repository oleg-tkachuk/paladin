# Runbook: tenant throttled

Covers `PaladinTenantThrottled` in the backend chart's PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.tenantThrottled`).

| Fires when | Meaning |
|-----------|---------|
| the per-tenant limiter refuses more than 1 request/s for a tenant, over 10m, for 10m | A tenant is held at its request ceiling steadily, not in a burst. |

The per-tenant limiter (`internal/middleware/tenant_ratelimit.go`) allows
`middleware.rate_limit.requests_per_second × 60` requests per tenant per
trailing minute, on the api and admin planes. Past it, a call fails with
`ResourceExhausted` ("per-tenant request rate exceeded; retry shortly"), and
a unary call carries `Retry-After` with the seconds until the window has room.
Refused requests still count toward the window, so a client that retries
without waiting stays refused.

Two causes look the same from here: a client in a loop, and a limit below
the tenant's real workload.

## Signal source

`paladin_tenant_ratelimit_decisions_total{tenant_id, allowed}` — one
increment per request with a tenant; `allowed="false"` is a refusal. Calls
without a tenant (health checks, login) are not limited.

The `tenant_id` is the tenant the request acts on. A platform admin's call
on the data plane that names another tenant is counted against that tenant,
not the admin's own.

## Triage

1. **Who is calling, and what.** The api logs carry `tenant_id` on every line
   written while serving a request:

   ```
   kubectl --context=<ctx> -n paladin logs -l app.kubernetes.io/component=api --tail=2000 --prefix \
     | grep '<tenant_id>' | grep -o '"rpc":"[^"]*"' | sort | uniq -c | sort -rn | head
   ```

   One procedure dominating at a steady rate is usually a loop: a client
   polling, or retrying without honouring `Retry-After`. A spread across the
   tenant's normal calls is real load.

2. **Allowed against refused.** If the tenant's allowed rate sits flat at the
   ceiling while refusals climb, the client is pushing past it:

   ```
   sum by (allowed) (rate(paladin_tenant_ratelimit_decisions_total{tenant_id="<tenant>"}[5m]))
   ```

3. **Which credential.** If the tenant has several API tokens, the token
   usage (`APITokenService.GetUsage`) points at the busiest client.

## Mitigation

- **A looping client.** Have its owner fix it — back off on
  `ResourceExhausted`, honour `Retry-After`. Revoking its API token stops it
  at once when the owner cannot be reached and the loop is hurting the
  tenant's other clients.
- **A limit below the real workload.** Raise
  `middleware.rate_limit.requests_per_second`. It is one value for every
  tenant: there is no per-tenant override, so raising it lifts the ceiling
  for all of them.

## Escalation / notes

- `warning`: the limiter is doing its job, and the tenant's other clients
  share its window — a loop in one starves the rest. Escalate when the tenant
  reports its own work failing.
