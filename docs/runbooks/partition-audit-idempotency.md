# Runbook: partitioning `audit_log` and `idempotency_keys`

Migrations `041_audit_log_partition.sql` and `042_idempotency_keys_partition.sql`
convert two append-only-with-TTL tables into RANGE-partitioned tables so
retention becomes `DROP PARTITION` instead of a `DELETE` that leaves dead
tuples for VACUUM.

These migrations are a **table rewrite under `ACCESS EXCLUSIVE`** and must run
in a maintenance window. This runbook is the procedure.

## What changes

| Table | Partition key | Granularity | PK change |
|-------|---------------|-------------|-----------|
| `audit_log` | `at` | monthly | `entry_id` → `(entry_id, at)` |
| `idempotency_keys` | `expires_at` | daily | `(tenant_id, method, key)` → `(… , expires_at)` |

Each migration: builds a new partitioned parent, creates partitions covering
the existing data plus a forward window, a `DEFAULT` catch-all, copies every
row, drops the old table, renames the new one into place, and re-creates all
indexes, RLS (`audit_log`), and the tenant FK (`idempotency_keys`). It is one
transaction — it either fully succeeds or fully rolls back.

The composite PKs do not weaken any guarantee: `entry_id` is an app-generated
UUIDv7 (still unique), and an idempotency `(tenant, method, key)` is written
once with a single `expires_at`.

## Downtime

The window length is the **table-rewrite copy time**, proportional to row
count. `audit_log` is the one to size — `idempotency_keys` is kept small by
its purger. Estimate before the window:

```sql
SELECT relname, n_live_tup, pg_size_pretty(pg_total_relation_size(relid))
FROM pg_stat_user_tables WHERE relname IN ('audit_log','idempotency_keys');
```

A rewrite copies + re-indexes every row while holding `ACCESS EXCLUSIVE`
(writers block). Low millions of rows: seconds to low minutes. If `audit_log`
is large, run the `AuditLogPurger` to the target retention **first** (outside
the window) so the rewrite copies less.

## Procedure

1. **Dry-run on a production-data snapshot.** Restore the latest backup to a
   scratch instance and apply 041+042 there. Confirm it succeeds, time the
   copy, and run the post-checks below. **Do not skip this** — the data copy
   is the part no fresh-DB test exercises. (The structure + copy *are*
   covered on synthetic data by `internal/integration/partition_test.go`
   (`go test -tags integration -run TestPartitionRewrite`); the dry-run adds
   real volume + real timing.)
2. **Take a fresh backup** immediately before the window. Rollback = restore
   (the migrations are forward-only and their Down sections deliberately
   `RAISE`).
3. **Stop writers** (scale the API/worker deployments to 0, or put the plane
   in maintenance). Audit/idempotency writes must be quiesced.
4. **Apply** via the normal migrate path (the chart's `migrate` job).
   Goose runs 041 then 042.
5. **Post-checks** (see below).
6. **Restart writers.**
7. The `PartitionMaintainer` worker takes over partition lifecycle on its
   normal schedule — no manual partition management.

## Post-checks

```sql
-- Both tables are partitioned.
SELECT c.relname FROM pg_partitioned_table p JOIN pg_class c ON c.oid=p.partrelid
WHERE c.relname IN ('audit_log','idempotency_keys');

-- Row counts match the pre-window snapshot.
SELECT count(*) FROM audit_log;
SELECT count(*) FROM idempotency_keys;

-- Nothing unexpectedly piled into the default partition.
SELECT count(*) FROM audit_log_default;
SELECT count(*) FROM idempotency_keys_default;

-- Indexes, RLS, and the FK survived.
SELECT indexname FROM pg_indexes WHERE tablename='audit_log';
SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname='audit_log';
SELECT conname FROM pg_constraint WHERE conrelid='idempotency_keys'::regclass AND contype='f';
```

## Rollback

The migrations are forward-only; their `Down` raises rather than corrupt.
To revert, **restore the pre-window backup** (step 2) and redeploy the prior
image.

## Granularity note

Monthly (`audit_log`) and daily (`idempotency_keys`) are chosen for typical
TTLs (months / hours-to-days). If a real deploy's insert-rate metrics show a
month is too coarse for `audit_log` (very high daily volume → giant
partitions), switch the maintainer's `Period` to weekly — that is the only
knob, and new partitions follow the new granularity. Existing partitions are
unaffected. Tracked in `BACKLOG.md`.
