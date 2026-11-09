# Runbook: rate limits not shared, or failing open

Covers `PaladinRateLimitFailingOpen` in the backend chart's PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.rateLimitFailingOpen`).

| Fires when | Meaning |
|-----------|---------|
| a limiter decided requests in its own process, or admitted one without a decision, in the last 10m, for 5m | The per-tenant or per-token ceiling is not shared: each replica grants the full rate, so the real ceiling is the replica count times the setting. |

Both limiters count in the store `middleware.rate_limit.backend` names
([ADR-0046](../adr/0046-rate-limits-off-the-request-path.md)):

- `postgres` — each replica counts in memory and, every `sync_interval`,
  adds its counts to `rate_limit_buckets` and reads the totals back. A failed
  sync keeps the counts for the next one; until one works, the replica
  decides on its own counts.
- `valkey` — every call runs a script in Valkey. A call Valkey does not
  answer within `datastores.valkey.timeout` is decided in the process.

Either way the limit stays on per replica rather than being lifted, and the
first failure and the recovery are each logged once.

## Signal source

- `paladin_ratelimit_degraded_total{backend}` — calls decided in the process
  because the shared store did not answer (`internal/ratecount`).
- `paladin_tenant_ratelimit_fail_open_total{tenant_id}` and
  `paladin_api_token_ratelimit_fail_open_total{tenant_id}` — requests admitted
  with no decision at all. The counters above never return an error, so these
  should stay at zero; a rise means a limiter is wired to something else.

All three exist only after they have moved once; the rule reads an absent one
as zero.

## Triage

1. **Which backend.**

   ```
   sum by (backend) (increase(paladin_ratelimit_degraded_total[10m]))
   ```

2. **What the replicas logged.** The switch is logged once per outage:

   ```
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-api --tail=2000 | grep -E 'rate limits counting|valkey did not answer|rate limit sync'
   ```

3. **postgres: can the planes write `rate_limit_buckets`?** Each sync is one
   batch: an `INSERT … ON CONFLICT DO UPDATE` of the deltas, then a read of the
   totals.

   ```sql
   SELECT pid, state, wait_event_type, wait_event, now() - xact_start AS xact_age, left(query, 120)
     FROM pg_stat_activity
    WHERE query ILIKE '%rate_limit_buckets%' AND state <> 'idle'
    ORDER BY xact_age DESC NULLS LAST;
   ```

   Also check the database itself: connection limits reached
   (`db_client_operation_errors_total` on the Operations dashboard), a
   failover in progress, disk full.

4. **valkey: is it up and fast enough?** A call past `datastores.valkey.timeout`
   counts as no answer. The `valkey` row in the database card of `/health`
   says, per role, whether Valkey answers a ping and whether the limits are
   counted shared or per process. Check the Valkey pods,
   `valkey-cli --latency`, and whether the password Secret still matches.

5. **Is the sweeper keeping the table small?** The worker's
   `RateLimitBucketSweeper` deletes buckets older than five minutes:

   ```
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-worker --tail=500 | grep 'failed to sweep rate limit buckets'
   ```

## Mitigation

Restore the store. Postgres replicas pick the shared totals up on their next
sync and Valkey on the next call; nothing has to be reset.

There is no switch that makes the limiters fail closed, by design. Turning
`middleware.rate_limit.enabled` off does not help: it removes enforcement
without removing the cause.

## Escalation / notes

- `critical`: the per-tenant limit is the defence against one tenant's loop
  starving the others, and per-token limits are what token holders were
  promised.
- `rate_limit_buckets` is UNLOGGED: a database crash or failover empties it,
  which lifts each limit for at most one window and raises nothing here.
