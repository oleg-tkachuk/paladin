-- +goose NO TRANSACTION
-- +goose Up

-- 068: outbox depth-gauge index.
--
-- OutboxRunner.sampleDepth publishes the backlog gauges that answer "is the
-- dispatcher keeping up" — the measurement the admission-control decision
-- (ADR-0003: shedding at produce time would break outbox atomicity, so the
-- drain is the only lever) depends on:
--
--   SELECT count(*) FROM event_deliveries WHERE status = 'pending'
--   GROUP BY tenant_id
--
-- event_deliveries_ready_idx is partial on the same status but keyed on
-- next_attempt_at only, so it can find the pending rows and then has to visit
-- the heap for every one of them to read tenant_id. That is the backlog-sized
-- read this gauge performs on every sample — and it is sampled most often
-- exactly when the backlog is deep.
--
-- Keying the same partial predicate on tenant_id makes the grouping an
-- index-only scan. Deliberately a second index rather than widening the
-- existing one: event_deliveries_ready_idx is on the claim path
-- (FOR UPDATE SKIP LOCKED ordered by next_attempt_at) and appending a column
-- there would bloat the hottest index in the dispatcher for a gauge's benefit.
--
-- Churn is bounded by the partial predicate — rows leave the index when they
-- stop being pending, which they already do for event_deliveries_ready_idx, so
-- the delivery loop takes no new class of write.
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_deliveries_pending_tenant_idx
    ON event_deliveries (tenant_id)
    WHERE status = 'pending';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS event_deliveries_pending_tenant_idx;
