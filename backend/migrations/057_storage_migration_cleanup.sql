-- +goose Up
-- +goose StatementBegin
-- 057_storage_migration_cleanup.sql
--
-- ADR-0011 Phase 3 slice 2: retention-gated cleanup of the old (shared) copies
-- after a shared->dedicated migration completes. Slice 1 left the source objects
-- in place as a fallback; slice 2 deletes them once a retention window has
-- elapsed, so a bad migration can still be rolled back within the window.
--
-- State machine gains a post-completed tail:
--   ... -> verifying -> completed --(now >= cleanup_after)--> cleaned
-- `completed` is no longer terminal for the worker (it must run cleanup); the
-- terminal states are now `cleaned` and `failed`.
ALTER TABLE tenant_storage_migrations
    ADD COLUMN cleanup_retention_seconds BIGINT NOT NULL DEFAULT 86400,
    ADD COLUMN cleanup_after TIMESTAMPTZ,
    ADD COLUMN cleaned_at    TIMESTAMPTZ;

-- Extend the state CHECK with 'cleaned'.
ALTER TABLE tenant_storage_migrations DROP CONSTRAINT tenant_storage_migrations_state_check;
ALTER TABLE tenant_storage_migrations ADD CONSTRAINT tenant_storage_migrations_state_check
    CHECK (state IN ('provisioning', 'copying', 'rebinding', 'verifying', 'completed', 'cleaned', 'failed'));

-- The worker must still pick up 'completed' rows to run cleanup, so the "active"
-- set now excludes only the terminal states.
DROP INDEX IF EXISTS idx_tenant_storage_migrations_active;
CREATE INDEX idx_tenant_storage_migrations_active
    ON tenant_storage_migrations (updated_at)
    WHERE state NOT IN ('cleaned', 'failed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tenant_storage_migrations_active;
CREATE INDEX idx_tenant_storage_migrations_active
    ON tenant_storage_migrations (updated_at)
    WHERE state NOT IN ('completed', 'failed');
ALTER TABLE tenant_storage_migrations DROP CONSTRAINT tenant_storage_migrations_state_check;
ALTER TABLE tenant_storage_migrations ADD CONSTRAINT tenant_storage_migrations_state_check
    CHECK (state IN ('provisioning', 'copying', 'rebinding', 'verifying', 'completed', 'failed'));
ALTER TABLE tenant_storage_migrations
    DROP COLUMN cleanup_retention_seconds,
    DROP COLUMN cleanup_after,
    DROP COLUMN cleaned_at;
-- +goose StatementEnd
