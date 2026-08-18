# Runbook: tenant hits its quota while storing (almost) nothing

Symptom: uploads fail with Connect `ResourceExhausted` — e.g.
`tenant quota exceeded: 900 + 200 > 1000 (max_total_bytes)` — but the
tenant's actual object count and byte total are well under the cap. The
message always names the scope (`tenant` / `bucket`) and the cap that
rejected, so read it first: this runbook is about the `tenant` +
`max_total_bytes` / `max_object_count` case, where the counter has drifted
away from what is actually stored. A `bucket`-scoped or `*_per_day`
rejection is more likely a genuinely-hit cap.

## Why this happens

`quotas.usage_total_bytes` / `usage_object_count` are denormalised counters,
not a measurement. Two things write them:

| Writer | When | Behaviour |
|--------|------|-----------|
| `touchQuota` (object + multipart handlers) | after a successful promote | **increment only**, and errors are swallowed on purpose so a DB blip cannot undo a committed state transition |
| `worker.QuotaReconciler` | every `worker.jobs.quota_reconcile.interval` (default 15m) | recomputes both columns from live `objects`, and rolls the per-day counters at midnight UTC |

`middleware.QuotaSoftCheck` rejects uploads by comparing those columns
against `max_total_bytes` / `max_object_count`. Delete a thousand objects and
the increment path does nothing — the counter stays where it was. Without the
reconciler running, the counter is monotonic and every tenant eventually
wedges. That is the failure this runbook covers.

## Triage

1. **Is the reconciler configured?**

   ```bash
   kubectl -n <ns> get cm paladin-core -o yaml | grep -A 2 quota_reconcile
   ```

   `interval: 0` (or a missing block on a hand-rolled config) disables it.
   That is only safe for a deployment that sets no quotas at all.

2. **Is it running?** The job holds a lease named `paladin.QuotaReconciler` and
   ticks through `RunTicker`, so it reports the standard worker metrics:

   ```promql
   paladin_worker_last_run_timestamp_seconds{worker="quota_reconciler"}
   rate(paladin_worker_runs_total{worker="quota_reconciler",outcome="error"}[15m])
   ```

   A stale timestamp means the worker pod is down or the lease is held by a
   pod that is wedged — see [`worker-stalled.md`](worker-stalled.md).

3. **How far off is the counter?** Compare the enforced number against the
   live truth. The console's **Platform Stats** page (`/stats`) shows both
   side by side — "Enforced usage" on the Quotas card versus the object
   census table. In SQL:

   ```sql
   SELECT q.tenant_id, q.usage_total_bytes, q.usage_object_count,
          COALESCE(sum(o.size_bytes) FILTER (WHERE o.state = 'AVAILABLE'), 0) AS live_bytes,
          count(o.object_id)         FILTER (WHERE o.state = 'AVAILABLE')     AS live_count
     FROM quotas q
     LEFT JOIN objects o ON o.tenant_id = q.tenant_id
    WHERE q.tenant_id IS NOT NULL
    GROUP BY q.quota_id, q.tenant_id, q.usage_total_bytes, q.usage_object_count
   HAVING q.usage_total_bytes  <> COALESCE(sum(o.size_bytes) FILTER (WHERE o.state = 'AVAILABLE'), 0)
       OR q.usage_object_count <> count(o.object_id) FILTER (WHERE o.state = 'AVAILABLE');
   ```

   Run it on a BYPASSRLS role (`paladin_migrate` / `paladin_reaper`) — both tables
   are RLS'd, so `paladin_app` without a tenant GUC returns nothing.

## Fixes

- **Reconciler disabled or wedged** → set a non-zero
  `worker.jobs.quota_reconcile.interval` and make sure a worker pod is
  healthy. The next tick corrects every drifted row; no manual step needed.
- **Need it corrected now** (the reconciler's next tick is too far out for an
  outage) → `QuotaService.ResetUsage` zeroes one quota's counters, and the
  next reconcile tick refills them from live objects. Zeroing temporarily
  under-counts, which fails *open* — acceptable while unblocking a customer.
- **Counter keeps drifting despite a healthy reconciler** → the log line
  `corrected drifted quota usage` carries a `rows` count on every tick. A
  steady-state fleet reconciles 0. A persistently non-zero count means the
  increment path is losing writes faster than deletes explain; check for
  `touchQuota` errors in the api-plane logs.

## What each cap does

All four caps are enforced by `middleware.QuotaSoftCheck` on the gated upload
procedures, in both scopes:

| Cap | Meaning | Headroom |
|-----|---------|----------|
| `max_total_bytes` | bytes currently stored | yes |
| `max_object_count` | objects currently stored | no |
| `max_bytes_per_day` | bytes admitted today | yes |
| `max_objects_per_day` | objects admitted today | no |

Headroom (default 10%) applies to byte caps only: those are raced by
concurrent presigns whose sizes are known up front, so admitting a little
overshoot beats falsely rejecting. Count caps move one at a time and are
exact.

**Two scopes.** The caller's tenant row is checked first, then the row for the
bucket the upload's ObjectKey resolves to. The rejection message names which
one stopped the request (`tenant quota exceeded` vs `bucket quota exceeded`),
so a caller does not have to guess. Bucket-scope resolution costs one point
lookup on `object_keys`, skipped entirely when the client sent a canonical
`storageBackends/…` name that already carries the binding.

**Everything about the bucket scope fails open.** An unbound ObjectKey, a
disabled backend, a bucket with no quota row — all pass the interceptor and
let the handler report the real problem. A routing failure surfacing as
`ResourceExhausted` would tell a caller to delete data that is not the issue.

`RegenerateUploadUrl` re-binds an existing PENDING row, so it consumes no
slot against either count cap. It still honours the byte caps: those bytes
are already stored.
