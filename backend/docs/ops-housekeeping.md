# Background jobs and housekeeping

`serve worker` runs the jobs below. Each is a ticker named in the
`paladin_worker_*` metrics (`worker` label) and leased, so several worker
replicas do not run the same job at once. Keys are under `worker.jobs`;
defaults come from `internal/config/schema.cue`. A job whose interval or TTL is
`0` does not start.

| Job | What it does | Key | Default |
|---|---|---|---|
| `reconciler` | promotes `PENDING` objects whose bytes arrived; fails ones whose upload URL expired | `reconciler.interval`, `.min_object_age` | `30s`, `2h` |
| `bucket_reconciler` | creates and deletes buckets on their backend, from the outbox | `reconciler.interval` | `30s` |
| `multipart_reaper` | aborts multipart sessions older than the TTL | `housekeeping.multipart_ttl` | `72h` |
| `multipart_abort_drainer` | retries aborts S3 refused (`pending_multipart_aborts`) | `purge_drain.interval` | `1m` |
| `purge_drainer` | deletes the bytes of permanently deleted objects (`pending_purges`) | `purge_drain.interval` | `1m` |
| `lifecycle` | applies per-bucket CEL expiration rules | `lifecycle.enabled`, `.interval` | `true`, `30m` |
| `lifecycle_hard_delete` | after the cooling-off window, deletes the row, then the bytes (a failure stays queued for `purge_drainer`) | `housekeeping.hard_delete_after` | `0` (off) |
| `quota_reconciler` | recomputes quota usage from live objects; resets daily counters | `quota_reconcile.interval` | `15m` |
| `audit_purger` | deletes `audit_log` rows older than the TTL | `housekeeping.audit_log_ttl` | `8760h` |
| `operations_purger` | deletes terminal `operations` rows older than the TTL | `housekeeping.operations_ttl` | `336h` |
| `event_delivery_purger` | deletes delivered and failed `event_deliveries` rows whose last attempt is older than the TTL | `housekeeping.event_deliveries_ttl` | `336h` |
| `partition_maintainer` | creates `audit_log` (monthly) and `idempotency_keys` (daily) partitions ahead; drops expired ones | `housekeeping.interval` | `1h` |
| `idempotency_purger` | deletes expired idempotency keys | `housekeeping.interval` | `1h` |
| `tenant_rate_bucket_sweeper` | drops elapsed per-tenant rate-limit windows | `housekeeping.interval` | `1h` |
| `refresh_token_reaper` | deletes expired refresh tokens | `refresh_token_reap.interval` | `1h` |
| `api_token_purger` | deletes API tokens expired for longer than the grace | `api_token.interval`, `.expired_for` | `1h`, `168h` |
| `capability_purger` | deletes capabilities expired for longer than the grace | `capability.interval`, `.expired_for` | `1h`, `24h` |
| `storage-migration` | runs operator-started tenant storage migrations | `operations.interval` | `5s` |
| `stale_operation_reclaimer` | fails operations no worker has touched for `stale_after` | `operations.stale_after` | `15m` |
| `replication` | cross-backend replication; dry-run | `replication.enabled`, `.interval` | `false`, `5m` |

`housekeeping.interval` also paces the audit, operations and event-delivery
purgers, and the hard-deleter: its first sweep runs one interval after the
worker starts. A sweep with nothing past the window logs nothing; each object
it removes logs `hard-deleted`.

`hard_delete_after` is a retention decision, so the default stays `0`: a
soft-deleted object keeps its bytes until someone chooses how long. Production
should set 7–30 days, long enough to undo a mistaken delete and short enough
not to pay for bytes nobody can read. A test environment can use a day. The
first sweep after enabling it drains the whole backlog at once, and each
object it removes also emits `paladin.object.purged`, so a consumer counting
those events sees a burst. Alerts on
these metrics: [deploy/grafana](../../deploy/grafana/README.md); a stalled job:
[runbooks/worker-stalled.md](../../docs/runbooks/worker-stalled.md).

The rest of this page is sizing the two TTL-driven tables, `audit_log` and
`operations`.

---

## 1. The sizing formula

```
disk_budget = rows_per_day × bytes_per_row × ttl_days × overhead_factor
```

`overhead_factor` accounts for indexes + WAL + bloat between vacuums.
Empirically `1.6` works for `audit_log` (one BTREE on `at`, JSONB columns
stay close to row size) and `1.3` for `operations` (smaller, fewer
indexes).

Solve for `ttl_days`:

```
ttl_days = disk_budget / (rows_per_day × bytes_per_row × overhead_factor)
```

---

## 2. audit_log

### Measuring rows-per-day

```sql
SELECT count(*) AS rows,
       pg_size_pretty(sum(pg_column_size(audit_log.*))) AS approx_bytes
FROM audit_log
WHERE at >= now() - interval '24 hours';
```

The `approx_bytes` figure is a lower bound — divide by `rows` to get
bytes-per-row. Add 30% for index/wal overhead.

### Defaults

- `audit_log_ttl = 8760h` (365 days). Compliance default; cut to 90 days
  if your audit obligations don't require a full year.

### Worked example

A 200 RPS deployment that audits every mutation produces ~17M audit rows
per day. Average row ≈ 600 bytes (subject + action + JSONB before/after).

```
17_000_000 × 600 × 365 × 1.6 ≈ 5.96 TB
```

That's the disk you'd need at 365-day retention. Three knobs to bring it
down:

1. **Shorter TTL.** 90 days drops to ~1.47 TB.
2. **Compress before-json/after-json** at the application layer
   (deferred — see BACKLOG: "Audit-log encryption at rest").
3. **Partitions already drop whole**: a month older than the TTL goes with
   `DROP PARTITION`, not `DELETE`.

`audit_log` is partitioned by month on `at`. `partition_maintainer` drops a
month once all of it is older than `audit_log_ttl`, which costs nothing per
row; `audit_purger` deletes the rows older than the TTL inside the current
partitions in bounded batches.

---

## 3. operations

### Measuring rows-per-day

```sql
SELECT count(*) FILTER (WHERE state IN ('SUCCEEDED','FAILED','CANCELLED'))
       AS terminal_rows,
       count(*) FILTER (WHERE done_at >= now() - interval '24 hours'
                          AND state IN ('SUCCEEDED','FAILED','CANCELLED'))
       AS terminal_rows_today,
       pg_size_pretty(sum(pg_column_size(operations.*))) AS approx_bytes
FROM operations;
```

Operations rows are larger than audit rows because the `metadata` and
`response` JSONB blobs can carry batch summaries — 4-8 KiB typical, up to
64 KiB for big BatchDelete operations.

### Defaults

- `operations_ttl = 336h` (14 days). Long enough for a post-mortem on a batch
  job, short enough that the console's "recent failures" widget means recent.
  The audit log retains the RPC that started the operation for a year
  (`audit_log_ttl`); this row is the execution detail, which stops being
  interesting once it is stale. Raise it where batch post-mortems routinely
  run older than a fortnight — the cost is table size, quantified below.

### Worked example

A deployment that runs 1,000 BatchDelete + 500 BatchCopy operations per
day, average 3 KiB per row:

```
1500 × 3000 × 30 × 1.3 ≈ 175 MiB
```

Trivial. The default `housekeeping.interval` (`1h`) is fine.

---

## 4. Choosing the interval

`worker.jobs.housekeeping.interval` paces the bounded purgers (`audit_log`,
`operations`). Two regimes:

- **Low volume (< 100k rows/day per table):** `1h` is fine. WAL stays
  steady, vacuum has air.
- **High volume (≥ 1M rows/day per table):** `15m` keeps the per-tick
  batch small enough that the bounded `LIMIT 10000` clauses drain in 1-3
  loop iterations. Reduces lock-window on each transaction.

`audit_log` already partitions by month; `operations` does not, so at very
high volume its purge stays a bounded `DELETE`.

---

## 5. Health checks

### "Is the reaper keeping up?"

```sql
-- audit_log
SELECT pg_size_pretty(pg_total_relation_size('audit_log')) AS size,
       (SELECT count(*) FROM audit_log) AS rows,
       (SELECT min(at) FROM audit_log) AS oldest,
       now() - (SELECT min(at) FROM audit_log) AS age;
```

If `age` materially exceeds `audit_log_ttl + interval`, the reaper is
behind. Likely causes:

- `audit_log_ttl = 0` (disabled — by design or by config typo).
- DB load is preventing the bounded `DELETE` from completing within
  `interval`. Lower the interval to compensate.

### "Is `operations` accumulating terminal rows?"

```sql
SELECT state, count(*),
       min(done_at) AS oldest,
       max(done_at) AS newest
FROM operations
WHERE state IN ('SUCCEEDED','FAILED','CANCELLED')
GROUP BY state
ORDER BY 1;
```

Same diagnosis: if `oldest` is older than `operations_ttl + interval`,
investigate.

---

## 6. Disabling a reaper

Set the TTL knob to `0`:

```yaml
worker:
  jobs:
    housekeeping:
      audit_log_ttl: 0       # keep audit_log forever; partitions are still created ahead
      operations_ttl: 0      # keep operations forever
```

**Don't disable both for production.** Disable selectively:

- `audit_log_ttl: 0` is reasonable when your storage budget allows
  unlimited retention or you've wired a cold-storage export pipeline.
- `operations_ttl: 0` is reasonable for small deployments where the
  table never grows large enough to matter.

When `OperationsTTL <= 0`, the worker exits cleanly at startup (no
ticking, no DB load).

---
