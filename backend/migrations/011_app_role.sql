-- +goose Up

-- ─── Minimum-privilege runtime role ────────────────────────────────────────
--
-- Paladin runs as `paladin_app` at runtime. Migrations run as a separate
-- DDL-capable role (`paladin_migrate` by convention; whatever the operator
-- supplies via `datastores.postgres.migrate_dsn`). This split protects
-- against:
--
--   - SQL-injection that escapes parameter binding — the runtime role
--     cannot DROP / ALTER / TRUNCATE.
--   - Compromised pod credentials reading other databases or roles.
--   - Accidental DDL from a runtime code path that thought it was
--     running a migration.
--
-- Privileges granted to `paladin_app`:
--   - USAGE on schema `public`.
--   - SELECT / INSERT / UPDATE / DELETE on every existing user table.
--   - USAGE / SELECT on every existing sequence (for SERIAL/IDENTITY,
--     not used today but harmless).
--   - EXECUTE on every existing function (the bump_resource_version
--     trigger function et al.).
--
-- Privileges WITHOUT explicit grant — denied by default:
--   - DDL: CREATE, ALTER, DROP, TRUNCATE, REFERENCES, TRIGGER on tables.
--   - Schema-level CREATE (no new tables/functions/types).
--   - Role management (CREATEROLE, CREATEDB, BYPASSRLS, SUPERUSER).
--   - Replication, CONNECT to other databases, etc.
--
-- Future migrations inherit the same DML grants automatically via
-- ALTER DEFAULT PRIVILEGES — the operator does not need to re-run this
-- migration after each new schema migration.
--
-- Bootstrap order (see docs/db-roles.md for the full runbook):
--   1. DBA creates `paladin_migrate` (CREATEDB owner of the database) and
--      `paladin_app` (LOGIN, no other privileges).
--   2. Operator runs migrations using `migrate_dsn=postgres://paladin_migrate@…`.
--   3. This migration grants `paladin_app` the runtime DML set.
--   4. Service connects with `dsn=postgres://paladin_app@…`.

-- Idempotent CREATE ROLE — bootstrap-friendly. The DBA may have
-- pre-created the role with a password; we only ensure existence here.
--
-- StatementBegin/End wraps ONLY the DO block — goose splits the file
-- on `;` and would otherwise treat the inner `IF/END IF;` semicolons
-- as statement boundaries. The surrounding GRANT/REVOKE statements
-- have no inner semicolons and parse fine via the auto-splitter.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_app') THEN
        -- NOLOGIN by default so a misconfigured deploy can't connect
        -- without an explicit ALTER ROLE … WITH LOGIN PASSWORD '…' from
        -- the operator. This forces credential setup to be deliberate.
        CREATE ROLE paladin_app NOLOGIN;
    END IF;
END $$;
-- +goose StatementEnd

-- Schema-level: USAGE only (lets the role resolve names; no CREATE so
-- paladin_app cannot add tables/functions/types to public).
GRANT USAGE ON SCHEMA public TO paladin_app;

-- Existing tables — DML only. No TRUNCATE, no REFERENCES, no TRIGGER.
GRANT SELECT, INSERT, UPDATE, DELETE
    ON ALL TABLES IN SCHEMA public TO paladin_app;

-- Existing sequences — needed if a future migration adds SERIAL/IDENTITY.
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO paladin_app;

-- Existing functions — needed for bump_resource_version,
-- enforce_object_key_bucket_tenancy, enforce_user_settings_tenant, etc.
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO paladin_app;

-- Future-proofing: every NEW table/sequence/function created by the
-- migration role inherits the same DML grants. Without this, every new
-- migration would need to re-run the GRANTs above.
--
-- Important: DEFAULT PRIVILEGES are scoped to the role that creates the
-- object. We tie them to current_user — so the migration role at apply
-- time is the role they apply to. Operators running migrations as a
-- different role need to repeat this clause for that role.
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO paladin_app;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO paladin_app;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO paladin_app;

-- Belt-and-braces: explicitly REVOKE the privileges that a default
-- public-schema configuration would grant to PUBLIC (and therefore to
-- paladin_app via inheritance). PG ≥ 15 already revokes CREATE on public
-- from PUBLIC by default, but older clusters (or restored dumps) may
-- still carry it.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM paladin_app;

-- +goose Down

-- The Down path is intentionally narrow: revoke the grants, but leave the
-- role itself. Dropping the role would fail if `paladin_app` owns objects or
-- has active sessions, and the operator may have other databases granted
-- to the same role. Removing the role is a manual DBA decision, not a
-- migration rollback.

REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public FROM paladin_app;
REVOKE USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public FROM paladin_app;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM paladin_app;
REVOKE USAGE ON SCHEMA public FROM paladin_app;

ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM paladin_app;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM paladin_app;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE EXECUTE ON FUNCTIONS FROM paladin_app;
