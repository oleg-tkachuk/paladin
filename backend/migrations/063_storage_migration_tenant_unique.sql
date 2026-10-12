-- +goose NO TRANSACTION
-- +goose Up

-- One storage migration per tenant (062 removed the duplicates). The tenant
-- lock in StartStorageMigration serialises starts; this is the backstop that
-- holds whatever path inserts. Concurrently, in a file of its own
-- (CONVENTIONS.md).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tenant_storage_migrations_tenant_key
    ON tenant_storage_migrations (tenant_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS tenant_storage_migrations_tenant_key;
