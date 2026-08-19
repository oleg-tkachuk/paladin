# ADR-0005: CNPG Postgres HA ownership & verify-full TLS

- **Status:** Accepted
- **Context:** The BACKLOG item "CNPG HA replicas in the Paladin chart"
  assumed the Paladin Helm chart templates the CNPG `Cluster` and just needed
  an `instances`/sync/PDB block exposed. On inspection that premise is
  **false**: the Paladin chart owns no `Cluster` resource, and every DSN
  points cross-namespace at `paladin-postgresql-rw.database.svc.cluster.local`
  (namespace `database`). The CNPG `Cluster` is owned by the sibling
  **gitops** repo.

## Decision

1. **HA config lives in gitops, not the Paladin chart.** Postgres HA
   (`instances`, `minSyncReplicas`/`maxSyncReplicas`, synchronous quorum,
   the Cluster's PodDisruptionBudget, anti-affinity /
   topologySpreadConstraints) is set on the CNPG `Cluster` spec in
   gitops. Adding a competing `Cluster` template to the Paladin chart would
   create two owners for the same database — rejected. Recommended prod
   spec: **3 instances, `minSyncReplicas: 1`, `maxSyncReplicas: 1`**
   (sync quorum), a PDB with `minAvailable: 2`, and zone spread.

   **Implemented (2026-07-06):** gitops
   `overlays/do/paladin-postgresql-ha/` carries exactly this spec (3 instances,
   1-replica sync quorum, `enablePDB` for the CNPG-managed ~`minAvailable: 2`,
   zone-key anti-affinity, prod resources/storage). The DO overlay repoints
   the `paladin-postgresql` Application at it; the local overlay stays on the
   single-instance base — HA is **prod-only** so orbstack/minikube keep
   `instances: 1`. The 1-replica sync quorum is the no-data-loss-on-failover
   guarantee: a commit is not acked until the WAL is flushed on the primary
   *and* a standby, so a primary/node kill loses nothing acknowledged
   (durability prioritised over availability — writes block, never silently
   drop, if no sync standby is reachable).

2. **App-side TLS is verify-full in prod** (this *is* in the Paladin chart).
   The chart defaults to `sslmode=require` (encrypt only); the prod
   overlay upgrades to `verify-full` to authenticate the server cert,
   mounting the CNPG CA via the new `postgresCa.{secretName,mountPath}`
   value (`/etc/paladin/pg-ca/ca.crt`). The CNPG `<cluster>-ca` secret must be
   synced into the Paladin namespace (cross-namespace secret mounts aren't
   allowed).

## Consequences

- The Paladin chart's responsibility for Postgres is the **client** posture
  (pool sizing, timeouts, TLS verification, CA mount) — not the cluster
  topology. The BACKLOG entry is corrected to reflect this split.
- HA changes are reviewed and rolled out in gitops; Paladin only needs the
  `-rw`/`-ro` service DNS to stay stable.
- Open (separate item): PgBouncer in front of CNPG for HPA-burst pool
  headroom — also a gitops / cluster concern.
