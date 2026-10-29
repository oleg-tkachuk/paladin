# Database — Paladin

Postgres is the control plane's only durable store: object metadata, tenancy,
policy, capabilities, the billing ledger, and the event outbox all live here.
S3 (or any S3-compatible backend) holds bytes and nothing else — every fact
Paladin reasons about is a row.

## Schema baseline

The schema starts from three ordered migrations that replaced the earlier
history; later changes are forward-only files on top of them
([CONVENTIONS.md](../migrations/CONVENTIONS.md)).

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

Capabilities are the exception: their records, revocations, meter, charges
and tenant budgets live in the `limes` schema, which
[limes/pgstore](https://github.com/oleg-tkachuk/limes/tree/main/pgstore)
creates and migrates itself (its own version table, `limes.schema_version`).
The migrate step runs it first, as the same role, then Goose: `076` grants
the runtime roles pgstore's tables and hangs Paladin's revocation triggers on
them, so they must exist before it runs.

### After the baseline

| File | Change |
|------|--------|
| `004` | RLS on `capability_revocations` |
| `005` | `tenant_budgets.max_budget_usd` NOT NULL |
| `006` | drop the unused `multipart_parts` |
| `007` | `object_locks` integrity fixes |
| `008` | OCC version on `tenant_budgets` |
| `009` | `tenant_rate_buckets`, the shared per-tenant rate limit |
| `010` | `pending_multipart_aborts`, multipart sessions S3 still has to abort |
| `011` | RLS on the tables `009` and `010` added |
| `012` | `refresh_tokens.superseded_at` — rotated versus revoked for cause |
| `013` | NOTIFY for the live audit stream |
| `014`, `015` | RLS on four IAM tables, then reverted: it broke login |
| `016`–`018` | the same RLS, one table group at a time: settings and bindings, `users`, `refresh_tokens` |
| `019`, `020` | `refresh_tokens.parent_id`, `first_used_at` and their index — lost-rotation recovery |
| `021` | NOTIFY `policy_changed` on a bucket policy change |
| `022`, `023` | `user_settings.theme` accepts the second dark palette (`ember`) |
| `024` | partial index on terminal `event_deliveries` for the retention purge |
| `025`–`028` | `pg_trgm`, then trigram and `jsonb_path_ops` GIN indexes on `objects` path, tags and metadata |
| `029` | `search_object_ids`, the `SECURITY DEFINER` search that lets those indexes work under RLS |
| `030`, `031` | `idx_objects_keyset_state` (keyset with `state` included) replaces `idx_objects_keyset` |
| `032` | `charge_refunds` |
| `033` | NOTIFY `capability_revoked` on every revocation (dropped by `077`) |
| `034` | `capability_reservations` and the `reserved_usd` counters |
| `035` | `objects.taint` |
| `036` | `capability_records.confirmation_jkt` — the DPoP key binding |
| `037` | DPoP proof ids shared across replicas (replaced by `060`) |
| `038` | `capability_biscuit_revocations` |
| `039` | `capability_copy_usage` and `charges.copy_ids` |
| `040`–`042` | `audit_log.resource_tenant_id`, its backfill and index |
| `043` | `rabbitmq` in the `event_sink_kind` enum |
| `044` | `bucket_quotas`: bucket caps move out of `quotas`, outside RLS ([ADR-0025](../../docs/adr/0025-bucket-quotas-are-platform-configuration.md)) |
| `045` | `idempotency_keys.request_hash`: a key reused for a different request is refused, not replayed |
| `046` | `checksum_part_size_bytes` beside a multipart object's composite checksum |
| `047` | NOTIFY `tenant_state` when a tenant is trashed, restored or removed |
| `048` | `storage_backend_features`, what the backend probe found per S3 feature (ADR-0026) |
| `049` | public buckets and collections (ADR-0027): `public_read`, `public_base_url`, `cache_control` and the triggers that fix them at creation |
| `050` | `buckets.created_on_backend` — only a bucket Paladin created may be deleted on its backend |
| `051`–`053` | `charges.external_ref`, `reservation_id`, `overrun` and their unique indexes |
| `054` | `capability_records.issued_by`, the issuing principal |
| `055`, `056` | money in nanos: unbounded `numeric` with nine-decimal CHECKs, then their validation |
| `057`, `058` | an ancestor's ceilings in columns; capability JSON rewritten to limes v0.4 spelling |
| `059` | `tenant_budgets.resource_version` defaults to 0 |
| `060` | `dpop_seen_proofs`, proof ids per key, replaces `dpop_seen_jti` |
| `061` | `tenants_purge_lock_guard`: a tenant holding a locked object cannot be purged |
| `062`–`064` | one storage migration per tenant: duplicates removed, a unique index replaces the plain one |
| `065` | storage-migration rollback (`rolling_back`, `rebound_at`, the rollback cursor) |
| `066` | ceilings: NULL is unlimited, 0 allows nothing |
| `067` | the ledger and the meter no longer reference capability records, so they outlive a purge |
| `068` | `dpop_seen_proofs` recreated, counted per holder |
| `069` | `tenant_budgets.period` and `charges.tenant_period` |
| `070` | quota caps nullable: NULL is no cap, 0 allows nothing |
| `071` | drop the unused `object_tags` |
| `072` | `api_tokens.rate_limit_rpm` nullable: NULL is no cap |
| `073` | `operations.progress`, out of `metadata` |
| `074` | `erased_objects`, tombstones of permanently deleted objects, kept as long as terminal operations |
| `075` | `objects.created_by_operation`, so a reissued batch copy can adopt its own `PENDING` rows |
| `076` | the capability tables are dropped, their rows with them; capabilities move to the `limes` schema |
| `077` | the `capability_revoked` triggers on the `limes` schema go; pgstore notifies revocations itself |
| `078` | `search_object_ids` takes the state filter as text and checks it itself |
| `079` | objects kept as FAILED are deleted and the label leaves `object_state` |
| `080` | every bucket gets an owner (`owner_tenant_id NOT NULL`) and sharing becomes `bucket_grants` (ADR-0032) |
| `081` | an object list whose filter names no state lists the live objects: `search_object_ids` takes `p_live_only` |
| `082` | `refresh_tokens` records the OAuth grant it came from: client, audience, scopes (ADR-0035) |
| `083` | `audit_log.relayed_by`, the OAuth client that relayed a call (ADR-0036) |
| `084` | `oauth_clients.self_registered` |
| `085` | `search_object_ids` sorts by the caller's `order_by`, with a keyset on (field, id) |
| `086` | `list_audit_entry_ids`, sorted and keyset-paged audit listing (`SECURITY INVOKER`) |
| `087` | `operations.initiator_subject` and its index, for the console's own-operations list |
| `088` | `operations.dismissed_at`: the initiator cleared it from their own list |
| `090` | `user_settings.theme` accepts three more dark palettes (`navy`, `graphite`, `bordeaux`) |
| `091` | `user_settings.theme` renames `ember` to `dusk`, carrying saved settings over |
| `092` | `audit_log.user_agent`, the client a call came from |
| `093` | `list_audit_entry_ids` sorts by `user_agent` too |
| `094` | `audit_log.after_json` is `request_json`, what it always held; the never-written `before_json` is dropped |
| `095` | `objects` gains an index answering a collection's usage — available objects' count, bytes, newest commit — on its own |
| `098` | `audit_log` drops `detail` and `outcome`, which nothing ever wrote |
| `104` | `dpop_seen_proofs` goes: the DPoP replay cache is pgstore's `limes.dpop_seen_proofs` |

Each file's header comment says why it exists.

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
`tenant_storage_migrations`. A tenant's slug is unique
among live tenants only; a soft-deleted tenant keeps its slug so the audit
trail stays resolvable, and renames are recorded in `tenant_slug_history`.

**Identity** — `users`, `user_settings`, `refresh_tokens`, `api_tokens`,
`api_token_rate_buckets`, `oauth_clients`, `oauth_authorization_codes`.

**Storage topology** — `storage_backends`, `storage_backend_health`,
`storage_backend_features`, `buckets`, `bucket_grants`, `replication_state`. A bucket row can exist before the physical
bucket does: `provision_state` carries the asynchronous provisioning.

**Namespaces** — `collections`. A collection is a logical namespace bound to
exactly one bucket. Objects address their collection by id; the *name* is a
segment of the storage path, which is why queries resolve names at the
boundary and carry ids inside.

**Objects** — `objects`, `object_versions`, `object_locks`,
`multipart_uploads`, `pending_multipart_aborts`, `pending_purges`,
`erased_objects`. State is the
`object_state` enum: `PENDING` → `AVAILABLE`, and `DELETED` for soft
deletion. An object that cannot become available is deleted, not kept in a
state of its own (`statemachine.DiscardFailed`; FAILED was dropped in 079). `objects.current_version_id` carries a deferred composite FK
to `object_versions (object_id, id)`, so the two rows can be written in
either order within one transaction but can never disagree at commit.

**Governance** — `quotas` (one per tenant), `bucket_quotas` (one per bucket,
deliberately not RLS'd: a bucket's cap is platform configuration, like the
bucket), `tenant_rate_buckets`. A quota cap of NULL is no cap and 0 is a cap
that allows nothing (`070`); before it, 0 meant "no cap", so a quota could not
stop all writes and a reader could not tell the two apart.

**Capabilities** — the `limes` schema, pgstore's: `capabilities`,
`revocations`, `biscuit_revocations` (revoked copies of a capability's
Biscuit, by block revocation id), `usage`, `copy_usage` (the counters of
Biscuit copies with limits of their own), `reservations` (open budget holds),
`charges` (the ledger billing reads), `tenant_budgets`, `exchanges` (the
capability each paladin-mcp token was exchanged for, so that a token yields
one, ADR-0041). A capability records the OAuth session its token was issued
in (`capabilities.session`), by which ending the grant revokes it. Every
table carries
its tenant and none refers to `tenants`: purging a tenant erases its rows
there through pgstore (`EraseTenant`), in the transaction that removes the
tenant row. Its capabilities are revoked, and those not yet past
`limes.MinPurgeRetention` stay on record, revoked, until the capability purger
takes them; its counters, holds, charges and budget go at once. The ledger
and the counters outlive a purged capability record.

pgstore migrates the schema itself (`pgstore.Migrate`, version table
`limes.schema_version`), before Goose — `076` refers to its tables — and grants
it to `paladin_app` and `paladin_reaper` after, once `002` has created them.

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
matches no row — the safe direction. Most policies also admit
`paladin_session_cross_tenant()` (the `paladin.cross_tenant` GUC) in `USING`
only, so an authorised platform-wide read can see every tenant while a write
stays pinned to one.

The `limes` schema has policies of its own, on settings pgstore sets per
transaction (`limes.tenant_id`, `limes.all_tenants`).
`capability/postgres.Scope` sets them from the same request context the
`paladin.*` settings come from: the acting tenant, or every tenant to read and
none to write for a platform-wide read, as `paladin.cross_tenant` does. Only
the capability purger scopes itself to write every tenant. Every store call
whose request names a tenant is checked against the scope, and one scoped to
another tenant fails as `limes.ErrScopeMismatch`. The
revocation checks the verifier makes before it knows a token's tenant are
`SECURITY DEFINER` functions of the schema, which read every tenant.

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
there is no tenant to scope a connection to. `bucket_quotas` and
`bucket_grants` are platform configuration of the same kind (`044`, `080`), and
`storage_backend_features` is an observation of a backend (`048`).

These policies are deliberately open:

- `api_tokens`, `users` and `refresh_tokens` allow a read while no session
  tenant is set (`*_preauth_read`), because token verification and login
  happen *before* the session tenant is known — that is what the lookup is
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
| `object_locks_enforce_retention` | `object_locks` | A lock row under legal hold or unexpired retention cannot be deleted; `GOVERNANCE` yields to `paladin.bypass_governance_retention` |
| `object_locks_enforce_no_weakening` | `object_locks` | A retention window may be extended, never shortened; `COMPLIANCE` has no bypass, `GOVERNANCE` needs the same bypass |
| `tenants_purge_lock_guard` | `tenants` | A tenant cannot be deleted while anything it owns is under legal hold or unexpired retention; runs before any foreign key, reads across RLS, same `GOVERNANCE` bypass |
| `collections_enforce_bucket_tenancy` | `collections` | A collection binds only to a bucket its tenant owns or holds a grant on (`bucket_grants`, ADR-0032), raising the named constraint `collections_bucket_tenancy` otherwise — RLS cannot catch this, because the inserted row carries the *attacker's* `tenant_id` and passes the policy cleanly |
| `buckets_public_read_immutable` | `buckets` | `public_read` and `public_base_url` are fixed at creation (`buckets_public_read_fixed`) |
| `collections_enforce_bucket_visibility` | `collections` | a collection's `public_read` equals its bucket's, is fixed at creation, and a public collection never moves bucket |
| `tenants_state_notify` | `tenants` | `pg_notify('tenant_state', '*')` on trash, restore or delete, so every replica clears its tenant-liveness cache |
| `*_notify_policy_changed` | `tenants`, `collections`, `buckets` | `pg_notify('policy_changed', …)` so the Cedar engine invalidates its cache |
| `multipart_uploads_abort_debt` | `multipart_uploads` | A cascade delete records the S3 abort it still owes in `pending_multipart_aborts` |
| `audit_log_notify` | `audit_log` | `pg_notify('paladin_audit', …)` for the live audit stream |
| `notify_revocation` (pgstore's) | `limes.revocations`, `limes.biscuit_revocations` | `pg_notify('limes_revocations', '')` so every replica clears its revocation caches (`pgstore.RevocationWatcher`) |

The policy-invalidation payload is a contract with
`internal/policy/cedar/store.go`: `"<tenant_uuid>"` for a tenant,
`"<tenant_uuid>:<collection>"` for a collection, and `"*"`
(`cedar.NotifyAllScopes`) for a bucket, whose collections span tenants. The guard compares policy
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

**Housekeeping** (see [ops-housekeeping.md](ops-housekeeping.md)) deletes
expired `PENDING` objects, aborts abandoned multipart sessions, purges
terminal `operations` and `erased_objects`, drops expired partitions, and drains `pending_purges`
and `pending_multipart_aborts`.

## Hand-written SQL

Most queries are generated by [sqlc](https://sqlc.dev) from
[`internal/store/postgres/queries/`](../internal/store/postgres/queries/).
A minority are hand-written where sqlc cannot express the statement —
dynamic DDL for partitions, census aggregates assembled from fragments.

sqlc validates its own queries against the schema at generate time. The
hand-written ones have no such gate, and that gap has produced real bugs
(a conflict target with no matching constraint; a column renamed everywhere
but one raw string). BACKLOG carries the item for a `PREPARE`-based check.
