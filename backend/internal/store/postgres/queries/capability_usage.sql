-- Per-capability runtime counters. Atomic UPSERT-and-check shape so
-- the hot path is a single round-trip with concurrency-safe semantics.
--
-- Naming dichotomy: the SQL columns retain their `_usd` suffixes for
-- historical reasons (avoiding sqlc regen + every-query churn). The
-- unit_code column added in migration 026 is the source of truth for
-- currency interpretation; the Go domain types use Amount + UnitCode.

-- name: BumpCapabilityRequestCount :one
-- Increments request_count by 1 and rejects when over the supplied cap.
-- max=0 means unlimited; we still write the row for spend tracking + UI.
INSERT INTO capability_usage (capability_id, request_count, spent_usd, unit_code, updated_at)
VALUES ($1, 1, 0, 'USD', now())
ON CONFLICT (capability_id) DO UPDATE
SET request_count = capability_usage.request_count + 1,
    updated_at    = now()
WHERE
    sqlc.arg('max_requests')::bigint = 0
    OR capability_usage.request_count + 1 <= sqlc.arg('max_requests')::bigint
RETURNING request_count;

-- name: ChargeCapability :one
-- Adds amount to spent_usd and rejects when over the supplied cap.
-- max_budget=0 means unlimited. unit_code is set on insert and
-- preserved on conflict (an existing row owns its currency).
INSERT INTO capability_usage (capability_id, request_count, spent_usd, unit_code, updated_at)
VALUES ($1, 0, sqlc.arg('amount_usd')::numeric, sqlc.arg('unit_code')::text, now())
ON CONFLICT (capability_id) DO UPDATE
SET spent_usd  = capability_usage.spent_usd + sqlc.arg('amount_usd')::numeric,
    updated_at = now()
WHERE
    sqlc.arg('max_budget_usd')::numeric = 0
    OR capability_usage.spent_usd + sqlc.arg('amount_usd')::numeric <= sqlc.arg('max_budget_usd')::numeric
RETURNING spent_usd;

-- name: GetCapabilityUsage :one
SELECT capability_id, request_count, spent_usd, unit_code, updated_at
FROM capability_usage
WHERE capability_id = $1;

-- name: DeleteCapabilityUsage :execrows
DELETE FROM capability_usage
WHERE capability_id = $1;

-- name: PurgeCapabilityUsageOrphans :execrows
-- Drops usage rows whose capability_id is no longer in capability_records.
-- Runs alongside CapabilityPurger so orphans don't accumulate when caps
-- are revoked/expired without going through DeleteCapabilityUsage.
DELETE FROM capability_usage
WHERE capability_id IN (
    SELECT u.capability_id FROM capability_usage AS u
    LEFT JOIN capability_records AS c ON c.id = u.capability_id
    WHERE c.id IS NULL
    LIMIT 10000
);
