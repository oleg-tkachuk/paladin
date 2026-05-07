-- +goose Up
-- +goose StatementBegin

-- ─── worker_leases — co-operative leader election for background jobs ───────
--
-- Replaces raw pg_advisory_lock for worker leadership. The trade-off:
-- advisory locks release automatically on session disconnect but never on
-- a stuck-but-alive process; this table makes the leader prove liveness by
-- renewing `expires_at`, and exposes a fence token (`generation`) so any
-- write the now-evicted leader still tries to commit can be rejected
-- atomically by the caller's WHERE clause.
--
-- One row per logical worker class (`name` PK). A pod may hold many
-- leases at once — that's expected when only one replica is up; on a
-- two-replica HA deploy with anti-affinity the leases distribute as
-- pods race to renew.
--
-- Sizing: rows are small and turnover is low (renew-in-place, no INSERT
-- churn). No autovacuum tuning needed.
--
-- TTL semantics: `expires_at = now() + lease_ttl`. Renewal cadence is
-- the caller's choice but should be ≤ TTL/3 so a single missed tick
-- doesn't lose the lease. Defaults in internal/worker/lease are 30s/10s.
--
-- Fence-token usage: workers thread `generation` through every UPDATE
-- they perform on tenant data. The pattern is:
--
--     UPDATE objects
--     SET    state = 'reaped'
--     WHERE  id = $1
--       AND  (SELECT generation FROM worker_leases WHERE name = 'reaper') = $2;
--
-- After a leadership change `generation` increments, and the previous
-- holder's pending writes silently no-op instead of corrupting state.

CREATE TABLE IF NOT EXISTS worker_leases (
    name         text PRIMARY KEY,
    holder_id    uuid NOT NULL,
    holder_meta  jsonb NOT NULL DEFAULT '{}'::jsonb,
    acquired_at  timestamptz NOT NULL DEFAULT NOW(),
    renewed_at   timestamptz NOT NULL DEFAULT NOW(),
    expires_at   timestamptz NOT NULL,
    generation   bigint NOT NULL DEFAULT 1
);

-- Convenience index for ops dashboards filtering by expiry.
CREATE INDEX IF NOT EXISTS worker_leases_expires_idx
    ON worker_leases (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS worker_leases;
-- +goose StatementEnd
