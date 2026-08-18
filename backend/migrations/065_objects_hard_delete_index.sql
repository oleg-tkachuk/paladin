-- +goose NO TRANSACTION
-- +goose Up

-- 065: lifecycle hard-deleter scan index.
--
-- ListHardDeletable (internal/worker/lifecycle_hard_delete.go) is the cooling-
-- off sweep that reclaims backend bytes after a soft delete:
--
--   WHERE state = 'DELETED' AND terminated_at IS NOT NULL
--     AND terminated_at < $cutoff AND NOT legal_hold AND …
--   ORDER BY terminated_at LIMIT n
--
-- No index covered `terminated_at`, so every tick sequentially scanned the
-- WHOLE objects table — live rows included, which are the overwhelming
-- majority — and then sorted. The scan grows with total objects while the
-- result set it is looking for grows only with deletions, so the job gets
-- steadily more expensive on a healthy fleet that deletes rarely.
--
-- The partial predicate is what makes this cheap: only soft-deleted rows with
-- a termination stamp are indexed, so the index stays proportional to the
-- pending-reclaim backlog, not to the table. Rows enter it on soft delete and
-- leave on hard delete — the churn is bounded by the delete rate, and the
-- upload path never touches it.
--
-- The remaining predicates (legal_hold, lock_mode/lock_retain_until) stay as
-- heap rechecks. They are cheap once the candidate set is this small, and
-- folding a `now()`-relative retention comparison into an index predicate is
-- not possible — index predicates must be immutable.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_hard_deletable
    ON objects (terminated_at)
    WHERE state = 'DELETED' AND terminated_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_hard_deletable;
