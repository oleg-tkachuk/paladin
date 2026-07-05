-- +goose Up

-- ─── Least-privilege cross-tenant reaper role ──────────────────────────────
--
-- The worker's background DML jobs (refresh-token / api-key / audit /
-- idempotency / operations / capability / api-token purgers, the lifecycle
-- soft/hard-delete reapers, the replication + reconciler sweeps, the event
-- dispatcher's outbox writer, the storage-migration copier) run cross-tenant:
-- they have NO per-request principal, so no `paladin.tenant_id` GUC is set, so an
-- RLS-scoped role sees zero rows and every reaper silently no-ops (the bug
-- migration 057's era fixed by moving them onto a BYPASSRLS pool).
--
-- Until now that BYPASSRLS pool reused `paladin_migrate` — the DDL owner. That is
-- far more privilege than a purger needs: a compromised worker credential
-- could DROP/ALTER/TRUNCATE the schema. `paladin_reaper` narrows it to exactly the
-- runtime DML set (identical to `paladin_app`, migration 011) but WITH bypassrls,
-- so background jobs get cross-tenant reach and nothing else.
--
-- Privileges granted here (mirrors paladin_app):
--   - USAGE on schema `public` (name resolution; no CREATE).
--   - SELECT / INSERT / UPDATE / DELETE on every user table.
--   - USAGE / SELECT on every sequence; EXECUTE on every function.
-- Denied by omission: all DDL (CREATE/ALTER/DROP/TRUNCATE), schema CREATE,
-- role management. The one background job that needs DDL — PartitionMaintainer
-- (CREATE/ATTACH/DROP PARTITION) — deliberately stays on the migrate role and
-- never connects as paladin_reaper.
--
-- The `bypassrls` + `login` + password attributes are set by the platform
-- (CNPG managed role, gitops cluster.yaml), NOT this migration — same split
-- as paladin_app / paladin_migrate. This migration only ensures the role exists
-- (NOLOGIN, so it can't connect before the platform grants a password) and
-- owns the DML grants, which belong with the schema they reference.
--
-- Bootstrap order: the CNPG managed role reconciles the role into existence
-- (login + password + bypassrls); this migration (run as paladin_migrate) grants
-- the DML set. Either order is safe — the CREATE ROLE guard below is a no-op
-- once CNPG has made the role, and the grants are idempotent.

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_reaper') THEN
        -- NOLOGIN: the platform (CNPG) grants LOGIN + password + BYPASSRLS.
        -- Creating it here NOLOGIN keeps the migration self-sufficient without
        -- minting a connectable credential from a migration.
        CREATE ROLE paladin_reaper NOLOGIN;
    END IF;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO paladin_reaper;

GRANT SELECT, INSERT, UPDATE, DELETE
    ON ALL TABLES IN SCHEMA public TO paladin_reaper;

GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO paladin_reaper;

GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO paladin_reaper;

-- Future tables/sequences/functions inherit the same DML set automatically.
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO paladin_reaper;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO paladin_reaper;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO paladin_reaper;

-- No schema CREATE — paladin_reaper is DML-only.
REVOKE CREATE ON SCHEMA public FROM paladin_reaper;

-- +goose Down

-- Narrow rollback: revoke the grants, leave the role (dropping it would fail if
-- it owns objects / has sessions, and removing a platform-managed role is a DBA
-- decision, not a migration rollback). Mirrors 011's Down.

REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public FROM paladin_reaper;
REVOKE USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public FROM paladin_reaper;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM paladin_reaper;
REVOKE USAGE ON SCHEMA public FROM paladin_reaper;

ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM paladin_reaper;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM paladin_reaper;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    REVOKE EXECUTE ON FUNCTIONS FROM paladin_reaper;
