-- Idempotency queries

-- name: GetIdempotencyKey :one
SELECT 
    tenant_id, idempotency_key, request_path, request_hash, 
    response_code, response_body, created_at, expires_at
FROM idempotency_keys
WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: UpsertIdempotencyKey :exec
INSERT INTO idempotency_keys (
    tenant_id, idempotency_key, request_path, request_hash, 
    response_code, response_body, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (tenant_id, idempotency_key) 
DO UPDATE SET 
    request_path = EXCLUDED.request_path,
    request_hash = EXCLUDED.request_hash,
    response_code = EXCLUDED.response_code,
    response_body = EXCLUDED.response_body,
    expires_at = EXCLUDED.expires_at;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys 
WHERE tenant_id = $1 AND idempotency_key = $2;
