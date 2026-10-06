# Runbook: Paladin background worker stalled / failing

Covers the two alerts in [`deploy/grafana/worker-alerts.yaml`](../../deploy/grafana/worker-alerts.yaml):

| Alert | Fires when | Meaning |
|-------|-----------|---------|
| `PaladinWorkerStalled` | `time() - paladin_worker_last_run_timestamp_seconds > 5 × paladin_worker_interval_seconds` for 2m | The worker has not completed a tick in over 5× its interval. |
| `PaladinWorkerTicksAllFailing` | only `outcome="error"` and zero `outcome="success"` in 15m | The loop is alive but every tick errors. |

Both carry a `worker` label (`reconciler`, `audit_purger`, `refresh_token_reaper`,
`api_token_purger`, `lifecycle`, `replication`, …) identifying which one.

## Signal source

Every periodic worker runs its tick through `internal/worker.RunTicker`, which
emits, per `worker` label:

- `paladin_worker_runs_total{worker,outcome}` — tick count, `outcome=success|error`.
- `paladin_worker_run_duration_seconds{worker}` — per-tick wall-clock histogram.
- `paladin_worker_last_run_timestamp_seconds{worker}` — unix ts of the last tick.
- `paladin_worker_interval_seconds{worker}` — configured interval (published at startup).

They leave the process like every other Paladin metric — pushed over OTLP or
scraped, per `otel.metrics_exporter`
([observability.md](../../backend/docs/observability.md)). On the worker pod
the scrape endpoint is `/metrics` on the ops listener (`worker.ops.addr`),
not `otel.metrics_addr`. With
`otel.enabled: false` they are no-ops, so the alerts only have data where
metrics are collected.

## Triage

1. **Which worker + how long.** Read the `worker` label and
   `time() - paladin_worker_last_run_timestamp_seconds` to see how far behind it is.

2. **Is the worker pod alive?** Workers run in the worker Deployment under
   co-operative **leader election** (one lease per job, `internal/worker/lease`).

   ```
   kubectl --context=<ctx> -n paladin get pods -l app.kubernetes.io/component=worker
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-worker --tail=200 | grep -i "<worker>"
   ```

   Look for `running job under lease` (claimed leadership) and the worker's own
   log lines. No `running job under lease` for that job ⇒ nobody holds the
   lease — see step 4.

   The lease is not named after the metric label: it is `paladin.` plus the
   job's Go type (`jobLeaseName` in `cmd/server/serve_worker.go`), so the
   `quota_reconciler` series belongs to lease `paladin.QuotaReconciler` and
   `reconciler` to `paladin.ReconcilerV2`. Grep the log's `name` field and the
   `worker_leases.name` column for that form.

3. **`PaladinWorkerStalled` — the loop is wedged.** A tick is blocked (slow/locked
   query, downstream S3/NATS hang, deadlock). Confirm with
   `paladin_worker_run_duration_seconds{worker=...}` (last bucket huge / no recent
   observation) and the DB:

   ```
   -- long-running / blocked statements
   SELECT pid, state, wait_event_type, wait_event, now()-query_start AS age, left(query,120)
   FROM pg_stat_activity WHERE state <> 'idle' ORDER BY age DESC LIMIT 20;
   ```

   Mitigation: clear the blocker (cancel the offending statement, restore the
   downstream). If the pod is wedged, `kubectl rollout restart deploy/paladin-core-worker`
   — another replica re-claims the lease and resumes. Restart is safe: every
   worker tick is idempotent (purges/reaps are `DELETE … WHERE`, reconciliation
   recomputes from source state).

4. **No lease holder.** If logs show the lease bouncing or never claimed, check
   the `worker_leases` table and that the worker pod can reach Postgres:

   ```
   SELECT name, holder_id, holder_meta, acquired_at, renewed_at FROM worker_leases ORDER BY name;
   ```

   A stale `renewed_at` with no new claim ⇒ all worker replicas are down or
   DB-partitioned. Restore worker pods / DB connectivity.

5. **`PaladinWorkerTicksAllFailing` — every tick errors.** The loop runs but the
   work fails. Grep the worker's logs for the error it logs each tick. Common causes: a missing/blocked
   migration, revoked DB grant, a downstream (S3 / NATS) outage, or a poison row.
   Fix the root cause; the next tick clears the alert.

## Escalation / notes

- These are `warning`s, not pager-pages — background maintenance lagging is
  degraded, not down. Escalate if the stalled worker is `replication` (RPO
  impact) or `audit_purger` (unbounded `audit_log` growth) and it stays firing.
- A worker disabled by config (`interval <= 0`) emits **no** series and cannot
  alert — that's intentional (`RunTicker` returns early). If you expect a
  worker to run but see no `paladin_worker_*{worker=...}` series at all, check its
  interval config rather than this runbook.
