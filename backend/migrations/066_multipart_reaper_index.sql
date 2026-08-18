-- +goose NO TRANSACTION
-- +goose Up

-- 066: multipart-reaper scan index.
--
-- ListStaleMultipartUploads (the abandoned-upload reaper, housekeeping
-- multipart_ttl) selects `WHERE m.created_at < $cutoff ORDER BY m.created_at`
-- and joins out to objects + object_keys for the routing it needs to abort the
-- upload on the backend.
--
-- multipart_uploads had exactly two indexes — the upload_id primary key and
-- idx_multipart_uploads_object_id for the FK — so the reaper's age predicate
-- was a sequential scan plus a sort on every tick.
--
-- The table is small in the healthy case (only in-flight sessions), which is
-- precisely why this matters: it is small *because* the reaper works, and the
-- reaper is what stops abandoned sessions accumulating. When it falls behind —
-- a backend outage during a large multipart push — the table grows and the
-- unindexed scan gets slower exactly when it needs to catch up.
--
-- One entry per upload, dropped when the session completes or is aborted. No
-- effect on the object write path.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_uploads_created
    ON multipart_uploads (created_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_multipart_uploads_created;
