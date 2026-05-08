-- +goose Up
-- +goose StatementBegin

-- ─── Per-token rate limiting ────────────────────────────────────────────────
--
-- Two changes:
--
--   1. Add `rate_limit_rpm` to api_tokens. 0 = unlimited (default —
--      preserves existing behaviour). Positive value = max
--      requests-per-minute for that token, enforced by a sliding-
--      window counter.
--
--   2. Add `api_token_rate_buckets` — one row per (token_id,
--      bucket_start) where bucket_start is the start of a one-minute
--      window. Reads sum the current bucket + a weighted slice of the
--      previous bucket; that's the standard sliding-window-counter
--      approximation Redis Cell / Cloudflare's algorithm uses.
--
-- Why a TABLE and not Redis: same operational reasons capability
-- revocation lives in Postgres — the verifier already hits this DB on
-- the token-verify path (prefix lookup + argon2id), and adding one
-- atomic UPSERT keeps the round-trip count flat. No new stateful
-- system enters the dependency graph.
--
-- Bucket lifetime: 1 minute fixed. Rows older than 5 minutes are
-- dropped by the api_token purger — the verifier never reads back
-- that far (sliding window is 2 buckets).

ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS rate_limit_rpm integer NOT NULL DEFAULT 0;

-- Sanity: rate_limit_rpm must be non-negative. Postgres CHECK keeps
-- a buggy admin endpoint from corrupting the column.
ALTER TABLE api_tokens
    ADD CONSTRAINT api_tokens_rate_limit_rpm_nonneg
    CHECK (rate_limit_rpm >= 0);

CREATE TABLE IF NOT EXISTS api_token_rate_buckets (
    token_id     uuid NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (token_id, bucket_start)
);

-- The reaper sweeps rows past bucket_start + 5 min. BRIN keeps the
-- index small at scale (millions of buckets a day for a busy tenant).
CREATE INDEX IF NOT EXISTS api_token_rate_buckets_start_brin
    ON api_token_rate_buckets USING brin (bucket_start);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_token_rate_buckets;
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_rate_limit_rpm_nonneg;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS rate_limit_rpm;
-- +goose StatementEnd
