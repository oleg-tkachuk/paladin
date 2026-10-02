-- +goose NO TRANSACTION
-- +goose Up

-- The retention purge deletes delivered and failed rows by the time of their
-- last attempt. Partial, so the live pending queue is not in it; CONCURRENTLY,
-- and on its own, per CONVENTIONS.md: the dispatcher writes this table on
-- every event.
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_deliveries_terminal_idx
    ON event_deliveries (last_attempt_at)
    WHERE status IN ('delivered', 'failed');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS event_deliveries_terminal_idx;
