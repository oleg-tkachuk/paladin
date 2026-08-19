# ADR-0007: Postgres connection headroom & pooling ownership

- **Status:** Accepted
- **Context:** The BACKLOG item "Pool sizing × replicas — PgBouncer for
  HPA-burst headroom" flagged that the per-pod pool math
  (`pool.max_conns: 6`) holds at steady-state replica counts but can exceed
  CNPG's default `max_connections: 100` when the api Deployment scales to
  its HPA ceiling (`maxReplicas: 12`). New connections past the ceiling fail
  with `FATAL: sorry, too many clients already`.

  Per [ADR-0005](0005-cnpg-ha-ownership.md), the Paladin chart does **not** own
  the CNPG `Cluster` — it lives in the sibling **gitops** repo, and every
  DSN points cross-namespace at `paladin-postgresql-rw.database.svc.cluster.local`.
  So `max_connections` (and any PgBouncer deployment fronting the cluster) is
  gitops's to set, the same boundary ADR-0005 drew for HA.

## Decision

1. **The durable headroom fix lives in gitops, not the Paladin chart.** The
   two durable options — raise the CNPG `Cluster`'s `max_connections` to
   ≥300, or deploy PgBouncer (transaction pooling) in front of the cluster —
   are both properties of the database tier gitops owns. Adding a PgBouncer
   Deployment to the Paladin chart that re-points every DSN would split ownership
   of the connection path across two repos (the same two-owners anti-pattern
   ADR-0005 rejected for the `Cluster`).

2. **The Paladin chart owns a bounded, published connection budget.** The chart's
   contract is: a small per-pod pool (`pool.max_conns`) and a documented
   worst-case budget the operator can check against the cluster's
   `max_connections`. `values-prod.yaml` carries the budget table inline.

3. **Until gitops raises the ceiling, prod stays within 100.** With
   `pool.max_conns: 6`, the steady-state floor is ~72 (12 pods) and the
   migrate/bootstrap hook Jobs add a transient handful. The api HPA ceiling
   is the term that can breach 100, so prod MUST keep
   `api.maxReplicas × pool.max_conns + (non-api steady-state pods × pool) +
   job headroom ≤ cluster max_connections`. The operator picks one of:
   - gitops raises CNPG `max_connections` ≥300 (recommended; then the HPA
     ceiling is unconstrained by this budget), **or**
   - gitops fronts CNPG with PgBouncer (transaction pooling collapses N app
     connections to a small server pool), **or**
   - cap `api.maxReplicas` so the worst case fits under 100 (with
     `pool.max_conns: 6` and the ~36-connection non-api + jobs floor, that's
     `maxReplicas ≤ ~10`).

## Consequences

- No PgBouncer is added to the Paladin chart; the alarmist "WARNING" comment in
  `values-prod.yaml` is replaced by the budget table + this ADR pointer, so
  the constraint is an owned, documented operational contract rather than an
  open risk note.
- A handoff item for gitops (raise `max_connections` or add PgBouncer)
  remains, but it is correctly located in the repo that owns the database —
  same disposition as ADR-0005's HA recommendation.
- If Paladin ever vendors its own Postgres (no gitops), this ADR is revisited:
  a chart-owned PgBouncer becomes the right call because ownership would no
  longer be split.
