-- +goose NO TRANSACTION
-- +goose Up

-- 6.1 Create Privilege Role (NOLOGIN)
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_auditlog_reaper_role') THEN
        CREATE ROLE paladin_auditlog_reaper_role NOLOGIN BYPASSRLS;
    END IF;
END
$$;
-- +goose StatementEnd

GRANT DELETE ON audit_logs TO paladin_auditlog_reaper_role;

-- 6.2 Create Dedicated LOGIN User
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_auditlog_reaper_user') THEN
        CREATE ROLE paladin_auditlog_reaper_user WITH LOGIN PASSWORD 'secure-password' INHERIT;
    END IF;
END
$$;
-- +goose StatementEnd

GRANT paladin_auditlog_reaper_role TO paladin_auditlog_reaper_user;

-- +goose Down
DROP ROLE IF EXISTS paladin_auditlog_reaper_user;
DROP ROLE IF EXISTS paladin_auditlog_reaper_role;
