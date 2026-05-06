-- +goose Up
-- +goose StatementBegin

-- ─── DeleteBucket outbox ────────────────────────────────────────────────────
--
-- Mirror the CreateBucket outbox (012) for the symmetric DELETE side.
-- Previously DeleteBucket called the backend's DeleteBucket inline and
-- then DELETEd the row in two unguarded steps:
--
--   ① s3.DeleteBucket → ② DELETE FROM buckets WHERE …
--
-- Same orphan window as the create path — if step ② failed (e.g.
-- another goroutine UPDATEd the row between get and delete and the
-- resource_version check rejected us) the physical bucket was already
-- gone but the row claimed it still existed; lifecycle / replication
-- workers would then NOT-FOUND on every iteration. The reverse
-- (`delete_on_backend=false`) leaves the row gone but the bucket
-- linguishing in S3 — invisible to PALADIN for cleanup.
--
-- New flow: the handler flips provision_state='deleting' (and clears
-- provision_attempts so the new operation gets the full retry budget).
-- The bucket-reconciler worker picks up 'deleting' rows, calls
-- s3.DeleteBucket idempotently (it swallows NoSuchBucket), and then
-- physically DELETEs the row. On a non-retryable error it parks the
-- row in 'deletion_failed' for operator triage.
--
-- 'deleting' rows stay visible to ListBuckets / GetBucket so the UI
-- can render a "Deleting…" badge — they're not write-targets but the
-- row's identity is still meaningful while convergence is in flight.

ALTER TABLE buckets
    DROP CONSTRAINT IF EXISTS bucket_provision_state_check;

ALTER TABLE buckets
    ADD CONSTRAINT bucket_provision_state_check
        CHECK (provision_state IN (
            'pending',
            'ready',
            'failed',
            'deleting',
            'deletion_failed'
        ));

-- The reconciler index (added in 012) only covered create-side states
-- — extend it. Drop and recreate so the predicate matches the new set.
DROP INDEX IF EXISTS idx_buckets_provision_pending;

CREATE INDEX IF NOT EXISTS idx_buckets_provision_in_flight
    ON buckets(last_provision_at NULLS FIRST, backend_id, bucket_name)
    WHERE provision_state IN ('pending', 'failed', 'deleting', 'deletion_failed');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_buckets_provision_in_flight;

-- Forcibly resurrect any rows that ended up in delete states — the v012
-- check would otherwise reject them on rollback.
UPDATE buckets
   SET provision_state = 'failed'
 WHERE provision_state IN ('deleting', 'deletion_failed');

ALTER TABLE buckets
    DROP CONSTRAINT IF EXISTS bucket_provision_state_check;

ALTER TABLE buckets
    ADD CONSTRAINT bucket_provision_state_check
        CHECK (provision_state IN ('pending', 'ready', 'failed'));

CREATE INDEX IF NOT EXISTS idx_buckets_provision_pending
    ON buckets(last_provision_at NULLS FIRST, backend_id, bucket_name)
    WHERE provision_state IN ('pending', 'failed');

-- +goose StatementEnd
