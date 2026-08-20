-- name: InsertAuditEntry :exec
INSERT INTO audit_log (
    id, at, actor_subject, actor_tenant_id, actor_audience,
    action, resource_name, request_id, source_ip,
    before_json, after_json, error_message, capability_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetAuditEntry :one
SELECT id, at, actor_subject, actor_tenant_id, actor_audience,
       action, resource_name, request_id, source_ip,
       before_json, after_json, error_message, capability_id
FROM audit_log
WHERE id = $1;

-- name: PurgeAuditOlderThan :execrows
-- Deletes audit_log rows older than the cutoff in batches of 10k. The
-- worker calls this in a loop until it returns 0 — keeps each statement
-- bounded so a long-overdue first-run doesn't lock the table for minutes
-- and bloat WAL with one giant DELETE. ctid-batched form is the canonical
-- Postgres pattern; idiom-equivalent to MySQL's `DELETE ... LIMIT`.
-- The inner SELECT aliases the table (`AS al`) and qualifies its column
-- references. Postgres parses the unaliased form fine — the column
-- unambiguously belongs to the inner FROM scope — but sqlc's parser
-- treats it as ambiguous and errors out at codegen.
DELETE FROM audit_log
WHERE ctid IN (
    SELECT al.ctid FROM audit_log AS al
    WHERE al.at < $1
    ORDER BY al.at
    LIMIT 10000
);

-- name: ListAuditEntries :many
-- Cursor: (at, id) tuple. Optional predicates use the canonical
-- sqlc OR-NULL idiom — caller passes NULL to opt out, Postgres
-- constant-folds the disabled branches at plan time.
--
-- The action_eq / action_prefix / at_gte / at_lte predicates are
-- populated by audith.applyAuditPushdown when the caller supplies a
-- CEL filter whose top-level conjuncts the extractor recognises. The
-- full CEL program ALWAYS still runs in-memory after this fetch, so
-- pushdown only narrows the candidate set; correctness lives in the
-- handler, not in this WHERE clause.
SELECT id, at, actor_subject, actor_tenant_id, actor_audience,
       action, resource_name, request_id, source_ip,
       before_json, after_json, error_message, capability_id
FROM audit_log
WHERE (sqlc.narg('actor_subject')::text IS NULL
       OR actor_subject = sqlc.narg('actor_subject')::text)
  AND (sqlc.narg('actor_tenant_id')::uuid IS NULL
       OR actor_tenant_id = sqlc.narg('actor_tenant_id')::uuid)
  AND (sqlc.narg('action_eq')::text IS NULL
       OR action = sqlc.narg('action_eq')::text)
  AND (sqlc.narg('action_prefix')::text IS NULL
       OR action LIKE sqlc.narg('action_prefix')::text)
  AND (sqlc.narg('at_gte')::timestamptz IS NULL
       OR at >= sqlc.narg('at_gte')::timestamptz)
  AND (sqlc.narg('at_lte')::timestamptz IS NULL
       OR at <= sqlc.narg('at_lte')::timestamptz)
  AND (sqlc.narg('after_at')::timestamptz IS NULL
       OR at < sqlc.narg('after_at')::timestamptz
       OR (at = sqlc.narg('after_at')::timestamptz AND id < sqlc.arg('after_id')::uuid))
ORDER BY at DESC, id DESC
LIMIT sqlc.arg('page_size');
