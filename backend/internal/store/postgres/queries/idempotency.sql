-- Idempotency-key queries. Scoped per (tenant, rpc method, key).

-- name: GetIdempotencyKey :one
-- LIMIT 1 keeps this :one-safe even if two concurrent first-writers raced
-- and each inserted a row (they differ only in expires_at — see Put). The
-- freshest live row wins; the loser ages out with its partition.
SELECT tenant_id, method, key, response, response_sha, created_at, expires_at
FROM idempotency_keys
WHERE tenant_id = $1 AND method = $2 AND key = $3
  AND expires_at > now()
ORDER BY expires_at DESC
LIMIT 1;

-- name: PutIdempotencyKey :exec
-- The ON CONFLICT target MUST match a real unique constraint. Migration 042
-- range-partitioned this table on expires_at, which widened the PK to
-- (tenant_id, method, key, expires_at) — a partitioned table requires the
-- partition key in every unique constraint, so a 3-column (tenant, method,
-- key) constraint cannot exist. Targeting the old 3-column tuple raised
-- 42P10 ("no unique or exclusion constraint matching the ON CONFLICT") on
-- EVERY write; the interceptor swallowed that error, so memoization silently
-- never happened and admin double-submits duplicated resources (FR-008).
--
-- We target the full PK with DO NOTHING. The interceptor's Get-before-Put
-- already short-circuits sequential retries (the common double-submit /
-- auto-retry case), so Put runs only on a genuine cache miss; DO NOTHING is
-- just a safety net for the astronomically-rare same-instant exact-PK race.
-- A retried Create after the prior row's TTL lapsed lands a fresh row (new
-- expires_at, new partition); GetIdempotencyKey filters the expired one and
-- the daily DROP PARTITION reclaims it — so the old expired-overwrite
-- DO UPDATE is no longer needed.
INSERT INTO idempotency_keys (tenant_id, method, key, response, response_sha, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, method, key, expires_at) DO NOTHING;

-- name: PurgeExpiredIdempotencyKeys :execrows
-- Bounded batch (10k). Worker loops until result is 0.
DELETE FROM idempotency_keys
WHERE ctid IN (
    SELECT ik.ctid FROM idempotency_keys AS ik
    WHERE ik.expires_at < now()
    ORDER BY ik.expires_at
    LIMIT 10000
);
