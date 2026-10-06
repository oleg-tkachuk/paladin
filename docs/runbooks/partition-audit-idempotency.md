# Runbook: the partitioned `audit_log` and `idempotency_keys`

Both tables are RANGE-partitioned from the schema baseline
([`001_initial_schema.sql`](../../backend/migrations/001_initial_schema.sql)),
so retention is `DROP PARTITION` instead of a `DELETE` that leaves dead tuples
for VACUUM. There is no conversion step: a database migrated from scratch
creates them partitioned.

## Shape

| Table | Partition key | Granularity | Primary key |
|-------|---------------|-------------|-------------|
| `audit_log` | `at` | monthly | `(id, at)` |
| `idempotency_keys` | `expires_at` | daily | `(id, expires_at)` |

Each has a `DEFAULT` catch-all (`audit_log_default`,
`idempotency_keys_default`), so an insert never fails for want of a
partition. `audit_log` is RLS'd (migration
[`002`](../../backend/migrations/002_roles_and_rls.sql)); `idempotency_keys`
carries the tenant FK (`ON DELETE CASCADE`).

## Who maintains the partitions

`PartitionMaintainer`
([`internal/worker/partition_maintainer.go`](../../backend/internal/worker/partition_maintainer.go),
worker label `partition_maintainer`) runs every
`worker.jobs.housekeeping.interval`. Per tick it keeps partitions
pre-created, the current one included — 3 monthly for `audit_log`, 8 daily
for `idempotency_keys` (`Ahead` in `internal/app/build_jobs.go`) — and
drops whole partitions whose range is past the retention cutoff: `worker.jobs.housekeeping.audit_log_ttl` for `audit_log`
(a non-positive TTL keeps it forever: create-ahead, never drop), expiry for
`idempotency_keys`. When rows already sit in `DEFAULT` for a range it is about
to create, it moves them into the new partition and attaches it in one
transaction.

The maintainer is an optimisation, not a correctness requirement: the
`DEFAULT` partition keeps inserts working and the `*Purger` workers still
bound both tables by `DELETE`, sweeping the `DEFAULT` partition and the live
boundary partition. A stalled maintainer shows as `PaladinWorkerStalled` with
`worker="partition_maintainer"` — see [`worker-stalled.md`](worker-stalled.md).
It needs the migrate role's table ownership for `CREATE` / `ATTACH` / `DROP`;
it runs on the partition pool, falling back to the reaper pool.

## Checks

```sql
-- Both tables are partitioned.
SELECT c.relname FROM pg_partitioned_table p JOIN pg_class c ON c.oid=p.partrelid
WHERE c.relname IN ('audit_log','idempotency_keys');

-- Rows in the default partition: the maintainer is not keeping ahead.
SELECT count(*) FROM audit_log_default;
SELECT count(*) FROM idempotency_keys_default;

-- Indexes, RLS, and the FK.
SELECT indexname FROM pg_indexes WHERE tablename='audit_log';
SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname='audit_log';
SELECT conname FROM pg_constraint WHERE conrelid='idempotency_keys'::regclass AND contype='f';
```

The shape and the maintainer are covered by
`backend/tests/integration/components/partition_test.go`
(`TestPartitionedTablesShape`, `TestPartitionMaintainerRoutesOutOfDefault`)
and `partition_maintainer_test.go` (`go test -tags integration`).

## Granularity

`PartitionPeriod` offers `PeriodDaily` and `PeriodMonthly` only. If
`audit_log` insert volume makes a month too coarse, a finer period is a code
change to `PartitionPeriod` and the `audit_log` spec in `build_jobs.go`;
existing partitions are unaffected and new ones follow the new granularity.
