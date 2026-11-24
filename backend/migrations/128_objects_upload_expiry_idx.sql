-- +goose NO TRANSACTION
-- +goose Up

-- The objects whose upload anchor the reconciler settles once their URL has
-- expired (127, ADR-0051): pending uploads and promoted objects alike, oldest
-- deadline first. Partial, so a settled object costs nothing. Concurrently,
-- in a file of its own (CONVENTIONS.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_upload_expiry_idx
    ON objects (presign_expires_at)
    WHERE upload_bucket_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS objects_upload_expiry_idx;
