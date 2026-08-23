-- Long-running operation queries.

-- name: CreateOperation :exec
INSERT INTO operations (id, tenant_id, type, state, metadata)
VALUES ($1, $2, $3, $4, $5);

-- name: GetOperation :one
SELECT sqlc.embed(operations)
FROM operations
WHERE id = $1 AND tenant_id = $2;

-- name: UpdateOperationState :execrows
-- Both uses of $state are cast explicitly. Without them Postgres deduces the
-- parameter's type twice — operation_state from the SET, text from the IN
-- comparison — and refuses the statement with 42P08 "inconsistent types
-- deduced for parameter". Every terminal transition failed on that: the runner
-- logged "operation succeeded" and then "failed to mark SUCCEEDED", leaving
-- every operation RUNNING forever and its response unwritten.
UPDATE operations
SET state         = sqlc.arg('state')::operation_state,
    metadata      = COALESCE(sqlc.narg('metadata'),      metadata),
    response      = COALESCE(sqlc.narg('response'),      response),
    error_code    = COALESCE(sqlc.narg('error_code'),    error_code),
    error_message = COALESCE(sqlc.narg('error_message'), error_message),
    done_at       = CASE WHEN sqlc.arg('state')::text IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
                         THEN now() ELSE done_at END,
    updated_at    = now()
WHERE id = $1;

-- name: ListOperations :many
SELECT sqlc.embed(operations)
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('state')::operation_state IS NULL OR state = sqlc.narg('state')::operation_state)
  AND (sqlc.narg('after_id')::uuid IS NULL OR id > sqlc.narg('after_id')::uuid)
ORDER BY id
LIMIT sqlc.arg('page_size');

-- name: PurgeTerminalOperations :execrows
-- Bounded batch (10k). Worker loops until result is 0. Uses
-- idx_operations_terminal_done_at (added in the schema baseline (001_initial_schema.sql)) so the planner
-- never scans the live PENDING/RUNNING tail.
DELETE FROM operations
WHERE ctid IN (
    SELECT op.ctid FROM operations AS op
    WHERE op.state IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
      AND op.done_at IS NOT NULL
      AND op.done_at < $1
    ORDER BY op.done_at
    LIMIT 10000
);

-- name: CancelOperation :execrows
UPDATE operations
SET state   = 'CANCELLED',
    done_at = now(),
    updated_at = now()
WHERE id = $1 AND tenant_id = $2
  AND state IN ('PENDING', 'RUNNING');
