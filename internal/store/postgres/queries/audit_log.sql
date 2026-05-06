-- name: InsertAuditEntry :exec
INSERT INTO audit_log (
    entry_id, at, actor_subject, actor_tenant_id, actor_audience,
    action, resource_name, request_id, source_ip,
    before_json, after_json, error_message
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetAuditEntry :one
SELECT entry_id, at, actor_subject, actor_tenant_id, actor_audience,
       action, resource_name, request_id, source_ip,
       before_json, after_json, error_message
FROM audit_log
WHERE entry_id = $1;

-- name: PurgeAuditOlderThan :execrows
-- Deletes audit_log rows older than the cutoff in batches of 10k. The
-- worker calls this in a loop until it returns 0 — keeps each statement
-- bounded so a long-overdue first-run doesn't lock the table for minutes
-- and bloat WAL with one giant DELETE. ctid-batched form is the canonical
-- Postgres pattern; idiom-equivalent to MySQL's `DELETE ... LIMIT`.
DELETE FROM audit_log
WHERE ctid IN (
    SELECT ctid FROM audit_log
    WHERE at < $1
    ORDER BY at
    LIMIT 10000
);

-- name: ListAuditEntries :many
-- Cursor: (at, entry_id) tuple. Filter args are intentionally simple — CEL
-- compiles to an in-memory pass after the SQL fetch.
SELECT entry_id, at, actor_subject, actor_tenant_id, actor_audience,
       action, resource_name, request_id, source_ip,
       before_json, after_json, error_message
FROM audit_log
WHERE (sqlc.narg('actor_subject')::text IS NULL
       OR actor_subject = sqlc.narg('actor_subject')::text)
  AND (sqlc.narg('actor_tenant_id')::uuid IS NULL
       OR actor_tenant_id = sqlc.narg('actor_tenant_id')::uuid)
  AND (sqlc.narg('after_at')::timestamptz IS NULL
       OR at < sqlc.narg('after_at')::timestamptz
       OR (at = sqlc.narg('after_at')::timestamptz AND entry_id < sqlc.arg('after_id')::uuid))
ORDER BY at DESC, entry_id DESC
LIMIT sqlc.arg('page_size');
