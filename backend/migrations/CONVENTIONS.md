# Migration conventions (forward-only, lock-aware)

Goose migrations in this directory are **append-only and immutable once
merged**: goose records a checksum per file, so editing an applied migration
makes every existing database refuse to start. Fixes go in a *new*
higher-numbered file, never by rewriting history.

## How migrations run in production (rollout gating)

`paladin migrate` (see `cmd/server/migrate.go`) is the only path that applies the
schema — the `serve` subcommands never migrate at startup. The Helm chart
runs it as a **`pre-install,pre-upgrade` hook Job** (`templates/job-migrate.yaml`,
`hook-weight: 0`), so:

```
migrate Job (weight 0) → bootstrap Job (weight 10) → Deployments roll out
```

The rollout is gated on the migrate Job succeeding. New pods never start
against an un-migrated schema, and there is **no version-skew window** where
a freshly-rolled pod races a half-applied migration — the new ReplicaSet is
created only after the hook completes.

The one residual cost is that a slow migration holds its locks while the
**old** pods are still serving (the pre-upgrade hook runs before the new
ReplicaSet). That is the motivation for the lock rules below.

## Lock rules for new DDL

Avoid `ACCESS EXCLUSIVE` locks that block reads/writes on a populated table
for more than a moment. Concretely:

- **Adding an index:** `CREATE INDEX CONCURRENTLY` (and `DROP INDEX
  CONCURRENTLY`). These cannot run inside a transaction, so the migration
  must be marked `-- +goose NO TRANSACTION` and must be in its **own file**
  (no other statements — a CONCURRENTLY failure leaves an INVALID index that
  a following statement in the same file would never reach to clean up).
- **Adding a column:** add it `NULL` with no default (or a *constant*
  default — Postgres ≥11 makes a constant default a metadata-only change).
  Never add `NOT NULL` + a volatile/computed default in one step against a
  populated table — that rewrites every row under `ACCESS EXCLUSIVE`.
- **Backfilling + enforcing NOT NULL:** split into three migrations —
  (1) add the nullable column, (2) backfill in batches (or an out-of-band
  job), (3) `ALTER … SET NOT NULL` after a `… ADD CONSTRAINT … CHECK (…)
  NOT VALID` + `VALIDATE CONSTRAINT` pair, which validates with a `SHARE
  UPDATE EXCLUSIVE` lock instead of a full-table `ACCESS EXCLUSIVE` scan.
  Migration `005_object_keys_bucket_required.sql` already follows the
  backfill-then-enforce shape; mirror it.
- **Type changes / rewrites:** prefer add-new-column + backfill + swap over
  an in-place `ALTER COLUMN … TYPE` that rewrites the table.
- For anything that is unavoidably a long table rewrite, run it **out of
  band** in a maintenance window (a one-off `paladin migrate`-style Job gated
  separately) rather than on the deploy hot path, and note it in the PR.

## Historical note — 004 / 006

`004_schema_normalization.sql` and `006_v2_planes.sql` `ALTER objects`
in-transaction without `CONCURRENTLY`. They predate this convention. They are
**not** rewritten (immutability rule above) and are a non-issue in practice:
both are long-applied in every existing database, so goose skips them, and on
a fresh install the `objects` table is empty so the locks are instant. The
convention above exists so no *future* migration reintroduces the pattern
against a populated table.
