-- +goose NO TRANSACTION
-- +goose Up

-- The objects a storage migration's catch-up copies, and its rebind checks
-- for: a tenant's objects whose bytes were written since the copy started
-- (116). Only rows promoted since 111 carry bytes_written_at, so the index
-- skips the rest. Concurrently, in a file of its own (CONVENTIONS.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_bytes_written_idx
    ON objects (tenant_id, bytes_written_at)
    WHERE bytes_written_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS objects_bytes_written_idx;
