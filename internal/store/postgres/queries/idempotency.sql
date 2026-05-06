-- Idempotency-key queries. Scoped per (tenant, rpc method, key).

-- name: GetIdempotencyKey :one
SELECT tenant_id, method, key, response, response_sha, created_at, expires_at
FROM idempotency_keys
WHERE tenant_id = $1 AND method = $2 AND key = $3
  AND expires_at > now();

-- name: PutIdempotencyKey :exec
INSERT INTO idempotency_keys (tenant_id, method, key, response, response_sha, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, method, key) DO NOTHING;

-- name: PurgeExpiredIdempotencyKeys :execrows
-- Bounded batch (10k). Worker loops until result is 0.
DELETE FROM idempotency_keys
WHERE ctid IN (
    SELECT ik.ctid FROM idempotency_keys AS ik
    WHERE ik.expires_at < now()
    ORDER BY ik.expires_at
    LIMIT 10000
);

-- name: CreateStorageBackend :exec
INSERT INTO storage_backends (id, kind, endpoint, region, events_enabled, events_target)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE
SET kind = EXCLUDED.kind,
    endpoint = EXCLUDED.endpoint,
    region = EXCLUDED.region,
    events_enabled = EXCLUDED.events_enabled,
    events_target = EXCLUDED.events_target;

-- name: GetStorageBackend :one
SELECT id, kind, endpoint, region, events_enabled, events_target, created_at
FROM storage_backends
WHERE id = $1;
