-- +goose NO TRANSACTION
-- +goose Up

-- The objects placed in a bucket: what a storage migration moves out of its
-- source, and what the foreign key's RESTRICT looks up when a bucket is
-- deleted (123). Concurrently, in a file of its own (CONVENTIONS.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_placement_bucket_idx
    ON objects (placement_bucket_id, tenant_id)
    WHERE placement_bucket_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS objects_placement_bucket_idx;
