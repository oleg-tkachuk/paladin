-- +goose Up
-- +goose StatementBegin

-- Per-capability runtime counters for caveat enforcement.
--
-- Two caveats become enforceable here:
--
--   MaxRequests   — capped via request_count. The interceptor does an
--                   atomic UPSERT-and-check: increments by 1, returns
--                   the new value, rejects when it exceeds the cap.
--                   Closed-by-default: a missing row treated as count=0
--                   (the UPSERT inserts on first hit).
--
--   MaxBudgetUSD  — capped via spent_usd. Handlers call Charge(amount)
--                   after each cost-emitting op. Same atomic UPSERT
--                   shape; over-budget rejects the charge so a handler
--                   that's accumulating cost can short-circuit.
--
-- Two columns rather than two tables: one row per capability ⇒ both
-- counters live on the same hot-path UPDATE, no second round-trip.
--
-- numeric(14,6) on spent_usd: 14 total digits, 6 fractional. Big
-- enough for $99,999,999.999999 worth of total spend per capability;
-- 6 fractional digits cover the smallest LiteLLM-reported costs
-- (~$0.0001 per 1k tokens).
--
-- No FK to capability_records: usage rows are short-lived (purged
-- by CapabilityPurger when the cap row is dropped). FK would force
-- an extra index lookup on every Insert and gain nothing.

CREATE TABLE capability_usage (
    capability_id  UUID         PRIMARY KEY,
    request_count  BIGINT       NOT NULL DEFAULT 0,
    spent_usd      NUMERIC(14,6) NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Reaper sweeps by updated_at when the parent capability is dropped.
-- Partial-on-non-zero keeps it small for the typical case where most
-- recently-created caps haven't been used yet.
CREATE INDEX idx_capability_usage_updated
    ON capability_usage (updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_capability_usage_updated;
DROP TABLE IF EXISTS capability_usage;
-- +goose StatementEnd
