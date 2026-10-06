# PostgreSQL role split — `paladin_migrate`, `paladin_app`, `paladin_reaper`

Paladin uses **three distinct database roles**:

| Role          | Used by                        | Privileges                                  |
|---------------|--------------------------------|---------------------------------------------|
| `paladin_migrate` | goose (schema migrations); the worker's partition maintenance | DDL on the Paladin database. Owns objects. `BYPASSRLS`. |
| `paladin_app`     | the running service (every request path) | DML only (`SELECT`/`INSERT`/`UPDATE`/`DELETE`). Subject to RLS. |
| `paladin_reaper`  | cross-tenant background work: worker jobs, the dispatcher's outbox drain, ingest | DML only. `BYPASSRLS`. |

The split is a defence-in-depth measure: a SQL-injection bug or a
compromised pod credential can only do what `paladin_app` is allowed to do —
which excludes `DROP TABLE`, `ALTER ROLE`, `TRUNCATE`, schema-level
`CREATE`, and role management.

One function is the exception to "DML only": `paladin_app` holds EXECUTE on
`search_object_ids` (migration 029), a SECURITY DEFINER function owned by
`paladin_migrate` that lets object search use its indexes under RLS. It
returns object ids only, for the session's own tenant, and the rows are
then read under `paladin_app`'s RLS as usual — see ADR-0019. A runtime role
not named `paladin_app` needs the same grant.

In **dev**, all three can be the same superuser — `migrate_dsn` defaults to
empty and goose runs as the runtime role. In **production**, the
`migrate_dsn` must point at `paladin_migrate` and `dsn` at `paladin_app`;
`reaper_dsn` should point at `paladin_reaper`, and falls back to
`migrate_dsn` when empty.

---

## 1. Bootstrap (one-time, by DBA)

Connect as a superuser. Replace the placeholder passwords with values
from your secret manager (Vault, AWS Secrets Manager, etc.).

```sql
-- Database owner / DDL role. BYPASSRLS: migrations and the cross-tenant
-- maintenance work must see every tenant's rows.
CREATE ROLE paladin_migrate WITH LOGIN PASSWORD '<from-secret>' BYPASSRLS;

-- Runtime role. NOLOGIN at first so misconfigured deploys can't connect
-- before the password is set.
CREATE ROLE paladin_app WITH NOLOGIN;

-- The worker's least-privilege cross-tenant DML role (reaper_dsn). The
-- migrations grant it DML whether or not it ever logs in.
CREATE ROLE paladin_reaper WITH NOLOGIN BYPASSRLS;

-- Create the database, owned by the migrate role.
CREATE DATABASE paladin OWNER paladin_migrate;

-- Grant connect rights to the runtime role.
GRANT CONNECT ON DATABASE paladin TO paladin_app;

-- Enable LOGIN + set password for the runtime role. Done in two steps so
-- the operator always has an explicit password-set moment in their
-- runbook.
ALTER ROLE paladin_app WITH LOGIN PASSWORD '<from-secret>';
```

That's all the manual SQL you need. Migration `002_roles_and_rls.sql` grants
`paladin_app` and `paladin_reaper` their DML when goose runs as
`paladin_migrate`. Only a superuser can grant `BYPASSRLS`, so the migration
only *attempts* it and tolerates being refused when it runs as
`paladin_migrate`. Without it nothing fails loudly: the worker, dispatcher and
ingest pools, which have no session tenant, silently see zero rows, because
RLS filters rather than errors.

---

## 2. What `paladin_app` can do

After migration `002` applies:

```sql
-- as paladin_app:
SELECT * FROM tenants;                   -- ✓
INSERT INTO objects (...) VALUES (...);  -- ✓
UPDATE users SET disabled = true ...;    -- ✓
DELETE FROM idempotency_keys WHERE ...;  -- ✓
```

What `paladin_app` **cannot** do:

```sql
-- as paladin_app:
DROP TABLE tenants;                      -- ✗ permission denied
ALTER TABLE objects ADD COLUMN ...;      -- ✗ permission denied
TRUNCATE TABLE audit_log;                -- ✗ permission denied
CREATE TABLE foo (id INT);               -- ✗ no CREATE on schema public
CREATE ROLE evil WITH LOGIN ...;         -- ✗ no CREATEROLE
GRANT ALL ON tenants TO PUBLIC;          -- ✗ not the table owner
```

Triggers continue to work: a trigger fires for every writer of its table,
whoever defined it — `bump_resource_version` still fires for `paladin_app`
even though `paladin_app` couldn't define it.

---

## 3. Adding new tables in future migrations

`ALTER DEFAULT PRIVILEGES` (set up by migration `002`) auto-grants the DML
set to `paladin_app` and `paladin_reaper` for every table the migration role
creates afterwards.
Migration authors **don't need to** repeat the GRANTs — they're inherited.

If a future migration creates an object via a different role (e.g. an
extension installed as superuser), the inheritance chain breaks — add an
explicit grant:

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON the_new_table TO paladin_app;
```

---

## 4. Configuration reference

```yaml
datastores:
  postgres:
    # Runtime: paladin_app (DML-only)
    dsn: "postgres://paladin_app@host:5432/paladin?sslmode=verify-full"
    password_secret:
      name: paladin-postgresql-app-user
      key: password
      namespace: database

    # Migrations: paladin_migrate (DDL-capable)
    migrate_dsn: "postgres://paladin_migrate@host:5432/paladin?sslmode=verify-full"
    migrate_password_secret:
      name: paladin-postgresql-migrate-user
      key: password
      namespace: database

    # Cross-tenant background DML: paladin_reaper (BYPASSRLS, no DDL)
    reaper_dsn: "postgres://paladin_reaper@host:5432/paladin?sslmode=verify-full"
    reaper_password_secret:
      name: paladin-postgresql-reaper-user
      key: password
      namespace: database
```

`migrate_dsn`, `reaper_dsn` and their password references are **optional**.
Empty `migrate_dsn` falls back to the runtime DSN — fine for local
development, unsafe for production. Empty `reaper_dsn` falls back to
`migrate_dsn`; with neither set, the background jobs run on the RLS-scoped
runtime pool and find zero rows (the worker logs a warning).

The two passwords MUST be stored in **separate** Kubernetes secrets so a
compromised RBAC binding granting access to the runtime secret does not
also expose the DDL credential. Operators following 12-factor split this
further into separate namespaces or even separate secret stores.

---

## 5. Deployment integration

### Helm

The chart builds `dsn`, `migrate_dsn` and their `password_secret`
references from its `postgres` block — see
[docs/install.md](../../docs/install.md). It does not template `reaper_dsn`;
set it under `config.datastores.postgres` or let it fall back to
`migrate_dsn`.
`helm upgrade --install` does not create the DB roles — that's a one-time DBA
step (§1).

### CI / preview environments

For ephemeral environments (preview deploys, CI), set `migrate_dsn` to
the same DSN as `dsn` and use a single superuser-equivalent role. The
role split is meaningful only when the runtime pool sits in a
production-like blast radius.

### Verifying the split

After deploy, `psql` as the runtime role and confirm DDL is denied:

```sh
psql "postgres://paladin_app:…@host/paladin" -c "DROP TABLE tenants;"
# ERROR:  must be owner of table tenants
```

Inversely, list grants:

```sh
psql "postgres://paladin_migrate:…@host/paladin" -c "\dp tenants"
# Access privileges
#       Schema |  Name   | Type  |       Access privileges       | …
#       public | tenants | table | paladin_migrate=arwdDxt/paladin_migrate
#                                  paladin_app=arwd/paladin_migrate
# (a/r/w/d = INSERT/SELECT/UPDATE/DELETE; absence of D/x/t = no
#  TRUNCATE/REFERENCES/TRIGGER for paladin_app)
```

---

## 6. Connection tagging

Every connection path sets `application_name` so DBAs can tell traffic
apart in `pg_stat_activity`:

```sql
SELECT application_name, count(*)
FROM pg_stat_activity
WHERE datname = 'paladin'
GROUP BY 1;
--  application_name | count
-- ------------------+-------
--  paladin              |    18    -- runtime queries
--  paladin-migrate      |     1    -- goose session (the migrate Job)
```

The cross-tenant pools add `paladin-dispatcher`, `paladin-ingest`,
`paladin-reaper` (worker jobs) and `paladin-worker-ddl` (partition
maintenance on the migrate role).

This is independent of the role split — even on dev where both share a
DSN the labels still differ.

---

## 7. What's NOT covered (yet)

- **Per-tenant DEKs / envelope encryption.** Tracked in BACKLOG.

- **Read-only role for analytics queries.** Trivial to add (`CREATE
  ROLE paladin_read; GRANT SELECT ON ALL TABLES …`) — not yet needed by any
  internal consumer.
