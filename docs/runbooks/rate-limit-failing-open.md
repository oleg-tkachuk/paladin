# Runbook: rate limiting failing open

Covers `PaladinRateLimitFailingOpen` in the backend chart's PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.rateLimitFailingOpen`).

| Fires when | Meaning |
|-----------|---------|
| either limiter admitted a request without a decision in the last 10m, for 5m | The per-tenant or per-token request ceiling is not being enforced. |

Both limiters keep their counts in Postgres and admit the request when the
count cannot be updated: a limiter outage must not become an API outage. The
cost is that the ceiling is gone while it lasts, and nothing on the request
path says so. **The limiters log nothing when they fail open** — this
counter is the only signal.

## Signal source

- `paladin_tenant_ratelimit_fail_open_total{tenant_id}` — the per-tenant
  limiter (`internal/middleware/tenant_ratelimit.go`), on the api and admin
  planes. Its buckets are `tenant_rate_buckets`, one row per tenant per
  minute, written through the RLS runtime pool.
- `paladin_api_token_ratelimit_fail_open_total` — the per-token limiter
  (`internal/auth/api_token_interceptor.go`), for API tokens created with a
  `rate_limit_rpm` above zero. Its buckets are `api_token_rate_buckets`.

Both counters exist only after a fail-open has happened once; the rule reads
an absent one as zero.

## Triage

1. **Which limiter, and which tenants.**

   ```
   sum by (tenant_id) (increase(paladin_tenant_ratelimit_fail_open_total[10m]))
   sum(increase(paladin_api_token_ratelimit_fail_open_total[10m]))
   ```

   Every tenant at once points at Postgres; one tenant points at its rows.

2. **Can the planes write the bucket tables?** The bump is an
   `INSERT … ON CONFLICT DO UPDATE` on every request. Look for what stops it:

   ```sql
   -- locks or long transactions on the bucket tables
   SELECT pid, state, wait_event_type, wait_event, now() - xact_start AS xact_age, left(query, 120)
     FROM pg_stat_activity
    WHERE query ILIKE '%rate_buckets%' AND state <> 'idle'
    ORDER BY xact_age DESC NULLS LAST;
   ```

   Also check the database itself: connection limits reached
   (`db_client_operation_errors_total` on the Operations dashboard), a
   failover in progress, disk full, or a migration holding the table.

3. **Is the sweeper keeping the tables small?** The worker's
   `TenantRateBucketSweeper` deletes old minutes; if it fails, the table
   grows and the bump slows:

   ```
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-worker --tail=500 | grep 'failed to sweep tenant rate buckets'
   ```

## Mitigation

Restore the database path — the bump succeeds again and enforcement resumes
on the next request; nothing has to be reset. If a tenant took advantage of
the gap, its limit is enforced from the next minute bucket on.

There is no switch that makes the limiters fail closed, by design. Turning
`middleware.rate_limit.enabled` off does not help: it removes enforcement
without removing the cause.

## Escalation / notes

- `critical`: the per-tenant limit is the defence against one tenant's loop
  starving the others, and per-token limits are what token holders were
  promised.
- A fail-open usually comes with other database alerts. When it is the only
  one, look at the bucket tables first.
