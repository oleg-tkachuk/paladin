-- Audit log queries

-- name: CreateAuditLog :exec
INSERT INTO audit_logs (
    id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
    client_ip, user_agent, method, path, query_params, request_headers,
    request_body_sha256, request_size_bytes, http_status, response_code,
    response_status, response_time_ms, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
);

-- name: GetAuditLog :one
SELECT 
    id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
    client_ip::text as client_ip, user_agent, method, path, query_params, request_headers,
    request_body_sha256, request_size_bytes, http_status, response_code,
    response_status, response_time_ms, created_at
FROM audit_logs
WHERE tenant_id = $1 AND id = $2;

-- name: ListAuditLogs :many
SELECT 
    id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
    client_ip::text as client_ip, user_agent, method, path, query_params, request_headers,
    request_body_sha256, request_size_bytes, http_status, response_code,
    response_status, response_time_ms, created_at,
    COUNT(*) OVER() AS total_count
FROM audit_logs
WHERE tenant_id = $1
  AND (sqlc.narg('from')::timestamptz IS NULL OR created_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz IS NULL OR created_at < sqlc.narg('to'))
  AND (sqlc.narg('path')::text IS NULL OR path = sqlc.narg('path'))
  AND (sqlc.narg('path_prefix')::text IS NULL OR path LIKE sqlc.narg('path_prefix') || '%')
  AND (sqlc.narg('method')::text IS NULL OR method = sqlc.narg('method'))
  AND (sqlc.narg('http_status')::int IS NULL OR http_status = sqlc.narg('http_status'))
  AND (sqlc.narg('request_id')::text IS NULL OR request_id = sqlc.narg('request_id'))
  AND (sqlc.narg('idempotency_key')::text IS NULL OR idempotency_key = sqlc.narg('idempotency_key'))
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor'))
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: PruneAuditLogs :execrows
DELETE FROM audit_logs
WHERE id IN (
    SELECT id FROM audit_logs AS al
    WHERE al.created_at < $1
    LIMIT $2
);
