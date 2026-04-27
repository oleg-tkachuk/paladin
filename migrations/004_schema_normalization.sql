-- +goose Up
-- +goose StatementBegin

-- ─── Schema normalization ──────────────────────────────────────────────────
-- Goals
--  1. Consistent FK column naming: object_keys.storage_backend → backend_id
--     (matches buckets.backend_id and the storage_backends.id parent column).
--  2. Tenant FK integrity for operations / idempotency_keys.
--  3. Indexing for cascade-delete paths and routing lookups.
--
-- Notes on idempotency
--  - sqlc applies every migration when computing its schema model but does
--    NOT execute anonymous PL/pgSQL DO blocks. Any column rename it should
--    "see" must therefore be expressed as a plain ALTER outside DO.
--  - The dropping of legacy FKs is wrapped in DO blocks (sqlc doesn't need
--    to "see" those — it only inspects column / table names).

-- 1. Drop legacy FKs that depend on the old column name. Wrapped to tolerate
--    repeated runs against partial-state DBs.
DO $$
BEGIN
    ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS object_keys_storage_backend_fkey;
    ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS object_keys_bucket_fk;
END $$;

DROP INDEX IF EXISTS idx_object_keys_storage_backend;

-- 2. Rename the column. Plain ALTER so sqlc picks it up.
ALTER TABLE object_keys RENAME COLUMN storage_backend TO backend_id;

-- 3. Re-add indexes and FKs against the new column name.
CREATE INDEX idx_object_keys_backend ON object_keys(backend_id);

ALTER TABLE object_keys
    ADD CONSTRAINT object_keys_backend_id_fkey
        FOREIGN KEY (backend_id) REFERENCES storage_backends(id) ON DELETE RESTRICT;

ALTER TABLE object_keys
    ADD CONSTRAINT object_keys_bucket_fk
        FOREIGN KEY (backend_id, bucket_name)
        REFERENCES buckets(backend_id, bucket_name)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED;

-- 4. Routing index: object_keys lookup by (tenant, backend, bucket) for
--    handlers that need the physical bucket given an Object reference.
DROP INDEX IF EXISTS idx_object_keys_bucket;
CREATE INDEX idx_object_keys_bucket_routing
    ON object_keys(tenant_id, backend_id, bucket_name);

-- ─── Tenant FK integrity for operations / idempotency_keys ────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_schema = 'public'
          AND table_name = 'operations'
          AND constraint_name = 'operations_tenant_id_fkey'
    ) THEN
        ALTER TABLE operations
            ADD CONSTRAINT operations_tenant_id_fkey
                FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id)
                ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_schema = 'public'
          AND table_name = 'idempotency_keys'
          AND constraint_name = 'idempotency_keys_tenant_id_fkey'
    ) THEN
        ALTER TABLE idempotency_keys
            ADD CONSTRAINT idempotency_keys_tenant_id_fkey
                FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id)
                ON DELETE CASCADE;
    END IF;
END $$;

-- ─── Performance indexes ──────────────────────────────────────────────────
-- multipart_uploads.object_id is a CASCADE FK target. Without an index on
-- this column, deleting an object scans multipart_uploads.
CREATE INDEX IF NOT EXISTS idx_multipart_uploads_object_id
    ON multipart_uploads(object_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_multipart_uploads_object_id;
ALTER TABLE idempotency_keys DROP CONSTRAINT IF EXISTS idempotency_keys_tenant_id_fkey;
ALTER TABLE operations DROP CONSTRAINT IF EXISTS operations_tenant_id_fkey;
DROP INDEX IF EXISTS idx_object_keys_bucket_routing;
ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS object_keys_bucket_fk;
ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS object_keys_backend_id_fkey;
DROP INDEX IF EXISTS idx_object_keys_backend;
ALTER TABLE object_keys RENAME COLUMN backend_id TO storage_backend;
-- +goose StatementEnd
