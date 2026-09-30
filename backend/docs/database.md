# Database — Paladin

Postgres is the control plane's only durable store: object metadata, tenancy,
policy, capabilities, the billing ledger, and the event outbox all live here.
S3 (or any S3-compatible backend) holds bytes and nothing else — every fact
Paladin reasons about is a row.

## Schema baseline

The schema ships as three ordered migrations rather than an accumulated
history:

| File | Contents |
|------|----------|
| [`001_initial_schema.sql`](../migrations/001_initial_schema.sql) | Types, tables, constraints, indexes |
| [`002_roles_and_rls.sql`](../migrations/002_roles_and_rls.sql) | Roles, grants, row-level security policies |
| [`003_triggers.sql`](../migrations/003_triggers.sql) | Triggers: resource versioning, immutability, lock enforcement, policy invalidation |

They replace the 65 incremental migrations that preceded them. The split is
by *kind*, not by date, so a reviewer can read the whole isolation story in
one file instead of reconstructing it from forty diffs.

**`001`'s down migration drops the schema.** Reprovision, do not migrate
backwards. Applying the baseline to a database that already holds data is
not supported — see [upgrading.md](../../docs/upgrading.md).

Migrations run under [Goose](https://github.com/pressly/goose) as
`paladin_migrate`, which holds DDL rights. The application role does not.

## Identity and naming

Every table follows [ADR-0017](../../docs/adr/0017-single-identity-model-and-naming.md):

- The primary key is always `id uuid`, never a natural key.
- `<entity>_id` names a foreign key and nothing else.
- Natural uniqueness is a `UNIQUE` constraint, not a primary key.
- `name` is the resource's own identifier within its parent; `display_name`
  is free text for humans.
- Fixed vocabularies are Postgres `ENUM`s, not `text` + `CHECK`.

One deliberate exception: `oauth_clients.client_id`, which is an identifier
the OAuth protocol itself defines.

## Table groups

**Tenancy** — `tenants`, `tenant_slug_history`, `tenant_default_bindings`,
`tenant_storage_migrations`, `tenant_budgets`. A tenant's slug is unique
among live tenants only; a soft-deleted tenant keeps its slug so the audit
trail stays resolvable, and renames are recorded in `tenant_slug_history`.

**Identity** — `users`, `user_settings`, `refresh_tokens`, `api_tokens`,
`api_token_rate_buckets`, `oauth_clients`, `oauth_authorization_codes`.

**Storage topology** — `storage_backends`, `storage_backend_health`,
`buckets`, `replication_state`. A bucket row can exist before the physical
bucket does: `provision_state` carries the asynchronous provisioning.

**Namespaces** — `collections`. A collection is a logical namespace bound to
exactly one bucket. Objects address their collection by id; the *name* is a
segment of the storage path, which is why queries resolve names at the
boundary and carry ids inside.

**Objects** — `objects`, `object_versions`, `object_locks`, `object_tags`,
`multipart_uploads`, `multipart_parts`, `pending_purges`. State is the
`object_state` enum: `PENDING` → `AVAILABLE` | `FAILED`, and `DELETED` for
soft deletion. `objects.current_version_id` carries a deferred composite FK
to `object_versions (object_id, id)`, so the two rows can be written in
either order within one transaction but can never disagree at commit.

**Governance** — `quotas`, `capability_records`, `capability_revocations`,
`capability_usage`, `charges`. Quota caps are `NOT NULL DEFAULT 0` where 0
means "no cap" — the convention every reader uses (`max_x > 0 AND usage_x >=
max_x`). Nullable caps would poison those comparisons three-valued.
`charges` captures `tenant_slug` at charge time: the ledger must stay
readable after a rename, and history is not rewritten by a later `UPDATE`
elsewhere.

**Events** — `event_subscriptions`, `event_deliveries`, `ingested_events`.
`event_deliveries` is the transactional outbox ([ADR-0003](../../docs/adr/0003-transactional-outbox.md)):
producers write it on the caller's transaction, and a dispatcher drains it.
`subscription_id` is a real FK with `ON DELETE CASCADE`, so deleting a
subscription takes its queued rows with it and an orphaned delivery cannot
be written at all. `ingested_events` deduplicates inbound storage events,
keyed `(source, event_id)` — two brokers may legitimately mint the same id.

**Operations** — `operations`, `audit_log`, `idempotency_keys`,
`worker_leases`.

## Partitioned tables

Two tables are partitioned by range, with a `DEFAULT` partition as the
catch-all:

| Table | Key | Period | Reclaimed by |
|-------|-----|--------|--------------|
| `audit_log` | `at` | monthly | `DROP PARTITION` past retention |
| `idempotency_keys` | `expires_at` | daily | `DROP PARTITION` past expiry |

`idempotency_keys` partitions on `expires_at` rather than `created_at`
deliberately: the partition key has to match the TTL semantics, or a whole
partition can never be dropped because one long-lived row sits in it.

Partitions are created by `PartitionMaintainer`, not by a migration — a
fresh database starts with only `DEFAULT`, and the maintainer relocates
rows out of it on its first tick. Indexes are declared on the partitioned
parent so every partition inherits them.

## Row-level security

RLS is a **primary** isolation control, not defence in depth. The data plane
runs as `paladin_app`, which has no `BYPASSRLS` — a missing policy is a
missing wall, and RLS filters rather than errors, so the failure is silent.

`FORCE ROW LEVEL SECURITY` is set on every protected table: without it the
table owner bypasses its own policies, and migrations run as the owner.

Policies read `tenant_id` directly from the row, which is why `tenant_id` is
denormalised onto every tenant-scoped table instead of being reached through
a join. The session's tenant comes from the `paladin.tenant_id` GUC, read
through `paladin_session_tenant_id()`; an unset GUC yields NULL, which
matches no row — the safe direction.

Three tables are isolated through their parent rather than a local
`tenant_id`: `multipart_parts` (via its upload), `capability_usage` (via its
capability), and nothing else. The duplication is for the hot path, not a
reflex.

### Acting on another tenant's behalf

The admin plane manages resources it does not own: a platform admin creating
a collection for tenant B writes a row whose `tenant_id` is B while its own
principal is bound to `platform`. Scoping the connection to the caller would
reject that write (`WITH CHECK`) and — worse — silently return nothing on the
matching read, because RLS filters rather than errors.

`auth.WithActingTenant` moves the connection's scope to the tenant being
acted on, and `EnableRLS` binds `paladin.tenant_id` from it. The rule is that
it is called only *after* the Cedar check that authorised this caller for
this tenant, and only with the tenant that check ran against — it is the
mechanism RLS otherwise denies, so the gate ahead of it is the protection.
It moves access rather than widening it: acting as B makes A's rows
invisible, which `tests/integration/components/acting_tenant_test.go` pins.

`tenants`, `storage_backends` and `buckets` stay uncovered for a different
reason: they are platform-level resources with no single owning tenant, so
there is no tenant to scope a connection to.

Two policies are deliberately open:

- `api_tokens` allows an unauthenticated read, because token verification
  happens *before* the session tenant is known — that is what the lookup is
  for.
- `audit_log` allows unrestricted `SELECT`. The audit trail is an operator
  surface; an investigation that can only see one tenant cannot answer "who
  touched this", which is the question the log exists for.

See [db-roles.md](db-roles.md) for the role split.

## Triggers

| Trigger | Table(s) | Purpose |
|---------|----------|---------|
| `bump_resource_version` | every versioned table | Optimistic concurrency: `resource_version` increments on write |
| `tenants_block_immutable_columns` | `tenants` | `id` and `slug` are immutable post-create |
| `object_locks_enforce_retention` | `object_locks` | A retention window may be extended, never shortened; `COMPLIANCE` has no bypass |
| `collections_enforce_bucket_tenancy` | `collections` | A collection may not bind to a bucket another tenant owns — RLS cannot catch this, because the inserted row carries the *attacker's* `tenant_id` and passes the policy cleanly |
| `*_notify_policy_changed` | `tenants`, `collections` | `pg_notify('policy_changed', …)` so the Cedar engine invalidates its cache |

The policy-invalidation payload is a contract with
`internal/policy/cedar/store.go`: `"<tenant_uuid>"` for a tenant,
`"<tenant_uuid>:<collection>"` for a collection. The guard compares policy
*text*, not its hash — the hash is computed by the application after the
write, so a hash-based guard is silent on the statement that changed the
policy.

## Data lifecycle

**Soft delete** moves an object to `DELETED` and stamps `terminated_at`. The
bytes stay in the backend and the row stays queryable, so restore is a state
change.

**Hard delete** removes the row and enqueues `pending_purges` in the same
transaction. Debt in `pending_purges` is the only remaining record of where
the bytes are, so a failed reclaim must stay owed rather than be dropped —
`PurgeDrainer` retries with backoff.

Object locks gate both: `legal_hold` and an unexpired `retain_until` block
the purge, and the worker has no governance bypass — `GOVERNANCE` is
honoured exactly like `COMPLIANCE` there.

**Housekeeping** (see [ops-housekeeping.md](ops-housekeeping.md)) reaps
expired `PENDING` objects, aborts abandoned multipart sessions, purges
terminal `operations`, drops expired partitions, and drains `pending_purges`.

## Hand-written SQL

Most queries are generated by [sqlc](https://sqlc.dev) from
[`internal/store/postgres/queries/`](../internal/store/postgres/queries/).
A minority are hand-written where sqlc cannot express the statement —
dynamic DDL for partitions, census aggregates assembled from fragments.

sqlc validates its own queries against the schema at generate time. The
hand-written ones have no such gate, and that gap has produced real bugs
(a conflict target with no matching constraint; a column renamed everywhere
but one raw string). BACKLOG carries the item for a `PREPARE`-based check.
