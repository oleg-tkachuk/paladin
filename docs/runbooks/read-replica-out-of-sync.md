# Runbook: read replica out of sync

Covers `PaladinReadReplicaOutOfSync` in the backend chart's PrometheusRule
([`_alerts-operations.tpl`](../../backend/deploy/chart/templates/_alerts-operations.tpl),
`metrics.alerts.rules.readReplicaOutOfSync`).

| Fires when | Meaning |
|-----------|---------|
| `max(paladin_db_replica_in_sync) == 0` for 15m | No pod has routed a read to the replica for 15m. |

With `datastores.postgres.replica.enabled`, object listings
(`ListObjects`, `CountObjects`, `ListDistinctTags`) read from a standby while
it is reachable and no more than `max_lag` behind. Otherwise they go to the
primary. Nothing fails while that lasts, which is why it needs an alert: a
replica can be lost for weeks while the primary quietly carries its load.
See [configuration.md](../configuration.md#read-replica).

## Signal source

Each pod probes the replica at start and then every `lag_check_period` (5s)
(`internal/store/postgres/replica.go`):

- `paladin_db_replica_in_sync` — 1 while this pod routes reads to it.
- `paladin_db_replica_lag_seconds` — the measured lag, when it could be
  measured.
- `paladin_db_replica_reads_total{served_by, reason}` — where each read went;
  `reason` is `in_sync`, `out_of_sync` or `replica_error`.

The series exist only where the replica is enabled; one pod still in sync
keeps the rule quiet.

## Triage

1. **Why the pods gave up on it.** The health page's `postgres-replica` row
   (never critical) says it in words:

   | Row | Cause |
   |-----|-------|
   | `unreachable, reads on the primary: <error>` | The pods cannot connect or query: the standby is down, its Service has no endpoints, or it refuses the credentials. |
   | `<lag> behind (max_lag <x>); reads on the primary` | Reachable but behind. |
   | `lag unknown; reads on the primary` | Reachable, but the lag query returned nothing: the node is not reporting replay. |
   | `not probed yet; reads on the primary` | The first probe has not finished. |

   The pods log the same when it changes: `read replica out of sync;
   routing reads to the primary`, with the error or the lag.

2. **Unreachable.** With an empty `replica.dsn` the chart derives the
   standby's host from the primary's, `<cluster>-rw` → `<cluster>-ro`.
   Check that Service has endpoints:

   ```
   kubectl --context=<ctx> -n <db-namespace> get endpoints <cluster>-ro
   ```

   **A single-instance CNPG cluster has none**: its `-ro` Service selects
   standbys, and there are no standbys. With the replica enabled there, this
   alert fires permanently. Add an instance, or turn the replica off.

3. **Behind.** Look at the standby's replay on the primary:

   ```sql
   SELECT application_name, state, sync_state, replay_lag, write_lag, flush_lag
     FROM pg_stat_replication;
   ```

   Lag that grows under write load is the standby's I/O or a long query on
   it holding replay back (`hot_standby_feedback`, `max_standby_streaming_delay`).
   Lag that sits just above `max_lag` (2s) is a threshold too tight for the
   cluster's normal replay delay.

## Mitigation

- Restore the standby or its Service; the next probe routes reads back, with
  no restart.
- If the lag is normal for the cluster, raise
  `datastores.postgres.replica.max_lag`. Listings then read data that may be
  that far behind a write.
- If the cluster cannot run a standby, set `replica.enabled: false`: the
  series go away with it.

## Escalation / notes

- `warning`: reads are correct on the primary, only its load is higher.
  Escalate when the primary is near its connection or CPU limits — it is now
  carrying every listing.
