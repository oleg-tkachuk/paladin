-- name: InsertAuditEntry :exec
INSERT INTO audit_log (
    id, at, actor_subject, actor_tenant_id, actor_audience,
    action, resource_name, request_id, source_ip,
    request_json, error_message, capability_id,
    resource_tenant_id, relayed_by, user_agent,
    state_before_json, state_after_json
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetAuditEntry :one
SELECT sqlc.embed(audit_log)
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
-- One page of the log in the order the caller names: list_audit_entry_ids
-- (086) applies the predicates, the order and the keyset on (field, id); this
-- reads those rows. ANY() keeps no order, so the caller sorts them by the
-- same key.
--
-- The action_eq / action_prefix / at_gte / at_lte predicates are populated
-- by audith.applyAuditPushdown when the caller supplies a CEL filter whose
-- top-level conjuncts the extractor recognises. The full CEL program ALWAYS
-- still runs in-memory after this fetch, so pushdown only narrows the
-- candidate set; correctness lives in the handler, not in this WHERE clause.
SELECT sqlc.embed(audit_log)
FROM audit_log
WHERE id = ANY (ARRAY(
        SELECT list_audit_entry_ids(
            sqlc.narg('actor_subject')::text,
            sqlc.narg('actor_tenant_id')::uuid,
            sqlc.narg('trail_tenant_id')::uuid,
            sqlc.narg('action_eq')::text,
            sqlc.narg('action_prefix')::text,
            sqlc.narg('at_gte')::timestamptz,
            sqlc.narg('at_lte')::timestamptz,
            sqlc.arg('order_by')::text,
            sqlc.arg('sort_desc')::boolean,
            sqlc.narg('after_value')::text,
            sqlc.narg('after_id')::uuid,
            sqlc.arg('page_size')::integer)));
