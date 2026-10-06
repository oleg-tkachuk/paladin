# Runbook: outbox not draining

Covers `PaladinOutboxNotDraining` in the backend chart's PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.outboxNotDraining`).

| Fires when | Meaning |
|-----------|---------|
| the minimum of `paladin_outbox_pending` over 15m stays above 500 rows, for 10m | Event deliveries are being written faster than the dispatcher clears them, or not cleared at all. |

Every state change that notifies subscribers writes an `event_deliveries` row
in the same transaction (ADR-0003); the dispatcher delivers them afterwards,
at least once. RPCs keep succeeding while it falls behind, so subscribers
learn late, or not at all, with nothing failing on the request path. The
minimum over the window is what the rule reads because depth alone spikes on
every write burst.

## Signal source

`OutboxRunner` samples the table at most every 30s
(`internal/worker/event_dispatcher.go`):

- `paladin_outbox_pending` — every `status = 'pending'` row, cluster-wide.
- `paladin_outbox_pending_max_per_tenant` — the deepest single tenant's
  pending rows. Neither carries a tenant label.

Pending includes rows that are not due yet: rows backing off after a failed
attempt (`next_attempt_at` in the future) and rows a tick has claimed (their
`next_attempt_at` is pushed 5m out while it works). A sink that keeps failing
holds its rows in pending until their attempt budget runs out, so the gauge
rises with a healthy loop and an unhealthy sink.

## Triage

1. **One tenant or all of them.** Compare the two lines on the Operations
   dashboard's *Outbox depth* panel. If the deepest tenant is most of the
   total, one tenant's subscriptions are the backlog; if not, the dispatcher
   itself is behind.

2. **Which subscriptions.** The dispatcher's stats list the worst
   subscriptions first, with their last error:

   ```
   kubectl --context=<ctx> -n paladin port-forward deploy/paladin-core-dispatcher 8099:8099
   curl -s localhost:8099/system/dispatcher-stats.json | jq '.pending, .failed, .oldest_pending_seconds, .subscriptions[:10]'
   ```

   The console shows the same on the home page (*Event dispatcher*), through
   `SystemService.GetDispatcherStats`.

3. **Is the dispatcher running and ticking?**

   ```
   kubectl --context=<ctx> -n paladin get pods -l app.kubernetes.io/component=dispatcher
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-dispatcher --tail=300 | grep -E 'outbox (tick failed|depth sample failed|backlog high)|failed to mark delivery row'
   ```

   | Line | Cause |
   |------|-------|
   | `outbox tick failed` | The claim query failed: Postgres is down, refusing the dispatcher's role, or the table is locked. |
   | `failed to mark delivery row` | A delivery finished but its status write failed; the row is retried. |
   | `outbox depth sample failed` | The gauges were not updated; the alert reads the last sample. |
   | `outbox backlog high` | One tenant has 10,000 pending rows: the warning this alert escalates. |

4. **What the rows say.** As an RLS-exempt role (`paladin_migrate`):

   ```sql
   SELECT tenant_id, subscription_id, count(*) AS pending,
          min(created_at) AS oldest, max(attempts) AS attempts,
          (array_agg(last_error ORDER BY last_attempt_at DESC NULLS LAST))[1] AS last_error
     FROM event_deliveries
    WHERE status = 'pending'
    GROUP BY 1, 2 ORDER BY pending DESC LIMIT 20;
   ```

   `attempts > 0` with a `last_error` is a sink refusing deliveries;
   `attempts = 0` on old rows is a dispatcher that is not reaching them.

## Mitigation

- **A sink refusing deliveries.** Fix the sink, or disable the subscription:
  its pending rows then fail at once instead of backing off for up to an hour
  each (`dispatcher.max_backoff`). Once the sink is back, rows that exhausted
  their budget are `failed` and wait for
  `EventSubscriptionService.RedriveFailedDeliveries` (the subscription's page
  in the console), which refuses a disabled subscription: enable it first.
  Redrive before `worker.jobs.housekeeping.event_deliveries_ttl` (14 days)
  purges them.
- **The dispatcher behind on healthy sinks.** Raise `dispatcher.batch_size`
  (50) or lower `dispatcher.poll_interval` (1s); the loop only sleeps when a
  tick found nothing, so a backlog it can reach drains at batch size per
  round trip. `dispatcher.charge_events_enabled` and
  `dispatcher.audit_mirror_enabled` add rows of their own; turn them off if
  they are the volume.
- **The dispatcher not ticking.** Restore its database access. Restarting it
  is safe: rows are claimed with `FOR UPDATE SKIP LOCKED`, and a claim a dead
  pod held expires after 5m.

Nothing is lost while the outbox is deep: rows stay until delivered or failed.

## Escalation / notes

- `critical`: subscribers act on these events, and a backlog that grows for
  long enough ages rows past their attempt budget into `failed`.
- Shedding at produce time is ruled out by design — it would break the
  transaction that makes the outbox reliable — so draining is the only lever.
