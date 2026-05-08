# Operator runbook — housekeeping tuning

PALADIN ships four reaper workers that keep tables bounded:

| Worker             | Targets             | Knob                                  |
|--------------------|---------------------|---------------------------------------|
| AuditLogPurger     | `audit_log`         | `workers.housekeeping.audit_log_ttl`  |
| OperationsReaper   | `operations`        | `workers.housekeeping.operations_ttl` |
| RefreshTokenPurger | `refresh_tokens`    | `workers.refresh_token_reap.interval` |
| ApiKeyExpirer      | `api_keys`          | `workers.api_key_reap.interval`       |

This runbook covers picking the **TTLs** for the two TTL-driven reapers
(`audit_log`, `operations`) so disk doesn't outgrow the budget.

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
3. **Partition by `at`** so old data drops via `DROP PARTITION` instead
   of `DELETE` (deferred — see BACKLOG: "audit_log partitioning by `at`").

Until partitioning lands, the bounded reaper purges 10k rows per call in
a loop until the day's batch is drained. WAL impact stays linear; vacuum
catches up between ticks.

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

- `operations_ttl = 720h` (30 days). Support window for reconstructing
  what happened around a failed batch.

### Worked example

A deployment that runs 1,000 BatchDelete + 500 BatchCopy operations per
day, average 3 KiB per row:

```
1500 × 3000 × 30 × 1.3 ≈ 175 MiB
```

Trivial. The reaper interval default (`6h`) is fine — you only need
faster ticking when the steady-state rate spikes.

---

## 4. Choosing the interval

`workers.housekeeping.interval` is shared across the bounded reapers
(`audit_log` + `operations`). Two regimes:

- **Low volume (< 100k rows/day per table):** `1h` is fine. WAL stays
  steady, vacuum has air.
- **High volume (≥ 1M rows/day per table):** `15m` keeps the per-tick
  batch small enough that the bounded `LIMIT 10000` clauses drain in 1-3
  loop iterations. Reduces lock-window on each transaction.

Beyond ~10M rows/day per table, **partition the table** (deferred BACKLOG
work). Reaping by `DROP PARTITION` is constant-time regardless of row
count and produces zero dead tuples.

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
workers:
  housekeeping:
    audit_log_ttl: 0       # KEEP audit_log forever (compliance preference)
    operations_ttl: 0      # KEEP operations forever (debugging preference)
```

**Don't disable both for production.** Disable selectively:

- `audit_log_ttl: 0` is reasonable when your storage budget allows
  unlimited retention or you've wired a cold-storage export pipeline.
- `operations_ttl: 0` is reasonable for small deployments where the
  table never grows large enough to matter.

When `OperationsTTL <= 0`, the worker exits cleanly at startup (no
ticking, no DB load).

---

## 7. Migration path to partitioning

Both `audit_log` and `idempotency_keys` are tracked in BACKLOG for
RANGE partitioning by their respective time columns. The migration is
non-trivial:

1. Stand up the partitioned table alongside the existing one.
2. Detach-and-reattach existing data into appropriate partitions
   (downtime ≈ minutes per ~10M rows on commodity hardware).
3. Swap reads/writes to the partitioned table.
4. `DROP TABLE` the original.

When that lands, `housekeeping.audit_log_ttl` becomes "drop partitions
older than this" — the loop-bounded `DELETE` goes away.
