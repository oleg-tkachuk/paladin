-- +goose NO TRANSACTION
-- +goose Up

-- 063's unique index on tenant_id serves every lookup the plain one did, so
-- the plain one only costs writes now. Concurrently, in a file of its own
-- (CONVENTIONS.md).
DROP INDEX CONCURRENTLY IF EXISTS tenant_storage_migrations_tenant_idx;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS tenant_storage_migrations_tenant_idx
    ON tenant_storage_migrations (tenant_id);
