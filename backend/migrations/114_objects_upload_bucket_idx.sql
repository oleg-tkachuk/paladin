-- +goose NO TRANSACTION
-- +goose Up

-- The uploads still anchored to a bucket (113): what a storage migration's
-- cleanup waits on, and what ON DELETE SET NULL finds when a bucket goes. The
-- promote clears the anchor, so only PENDING rows hold one and the index
-- stays that small. Concurrently, in a file of its own (CONVENTIONS.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_upload_bucket_idx
    ON objects (upload_bucket_id)
    WHERE upload_bucket_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS objects_upload_bucket_idx;
