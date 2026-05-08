-- +goose Up
-- +goose StatementBegin

-- ─── Bucket-provisioning outbox ─────────────────────────────────────────────
--
-- Goal: never have an orphan between the `buckets` table and the physical
-- backend. The previous BucketService.CreateBucket flow called the
-- provisioner first then INSERTed the row — if the DB write failed (e.g.
-- a unique-key violation under concurrent creates, an FK miss, or a
-- transient pgx error) the S3 bucket already existed with no DB record
-- to track it. Conversely, ProvisionOnBackend=false created a DB row
-- with no physical bucket, so reads would return a "ready" bucket that
-- lifecycle/replication workers couldn't touch.
--
-- This migration replaces the implicit two-phase commit with an outbox:
-- the row is the source of truth, and a reconciler worker drives the
-- physical state to match.
--
--   provision_state:
--     'pending' — caller asked for a bucket, no S3 attempt yet (or all
--                 prior attempts failed transiently). Reconciler picks up.
--     'ready'   — S3 confirms the bucket exists. Safe to bind ObjectKeys
--                 and accept writes. Also the state for buckets created
--                 with ProvisionOnBackend=false (operator pre-existing
--                 bucket).
--     'failed'  — non-retryable error (auth denied, quota, region
--                 mismatch). Reconciler stops touching it; the operator
--                 must intervene (Delete + recreate, or fix the backend
--                 and force-retry).
--
-- All existing rows are backfilled to 'ready' — they were created under
-- the old code path, which (with ProvisionOnBackend=true) would have
-- already returned an error if S3 didn't accept the bucket.
ALTER TABLE buckets
    ADD COLUMN IF NOT EXISTS provision_state    TEXT NOT NULL DEFAULT 'ready',
    ADD COLUMN IF NOT EXISTS provision_error    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provision_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_provision_at  TIMESTAMPTZ;

ALTER TABLE buckets
    DROP CONSTRAINT IF EXISTS bucket_provision_state_check;
ALTER TABLE buckets
    ADD CONSTRAINT bucket_provision_state_check
        CHECK (provision_state IN ('pending', 'ready', 'failed'));

-- Reconciler index: cheap predicate scan to grab the next batch of work.
-- `last_provision_at` is included so the worker can ORDER BY it (oldest
-- first) without an extra sort. Filtered to non-ready states only — the
-- vast majority of rows will be 'ready' and we don't want to bloat the
-- index with them.
CREATE INDEX IF NOT EXISTS idx_buckets_provision_pending
    ON buckets(last_provision_at NULLS FIRST, backend_id, bucket_name)
    WHERE provision_state IN ('pending', 'failed');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_buckets_provision_pending;

ALTER TABLE buckets
    DROP CONSTRAINT IF EXISTS bucket_provision_state_check;

ALTER TABLE buckets
    DROP COLUMN IF EXISTS last_provision_at,
    DROP COLUMN IF EXISTS provision_attempts,
    DROP COLUMN IF EXISTS provision_error,
    DROP COLUMN IF EXISTS provision_state;

-- +goose StatementEnd
