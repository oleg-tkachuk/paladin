-- +goose NO TRANSACTION
-- +goose Up

-- Superseded by idx_objects_keyset_state (030): same key columns, plus state.
-- Kept, it would cost every upload a second copy of the same B-tree.
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_keyset;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_keyset
    ON objects (tenant_id, collection_id, id);
