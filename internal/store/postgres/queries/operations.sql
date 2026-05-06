-- Long-running operation queries.

-- name: CreateOperation :exec
INSERT INTO operations (operation_id, tenant_id, type, state, metadata)
VALUES ($1, $2, $3, $4, $5);

-- name: GetOperation :one
SELECT sqlc.embed(operations)
FROM operations
WHERE operation_id = $1 AND tenant_id = $2;

-- name: UpdateOperationState :execrows
UPDATE operations
SET state         = sqlc.arg('state'),
    metadata      = COALESCE(sqlc.narg('metadata'),      metadata),
    response      = COALESCE(sqlc.narg('response'),      response),
    error_code    = COALESCE(sqlc.narg('error_code'),    error_code),
    error_message = COALESCE(sqlc.narg('error_message'), error_message),
    done_at       = CASE WHEN sqlc.arg('state') IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
                         THEN now() ELSE done_at END,
    updated_at    = now()
WHERE operation_id = $1;

-- name: ListOperations :many
SELECT sqlc.embed(operations)
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('state')::operation_state IS NULL OR state = sqlc.narg('state')::operation_state)
  AND (sqlc.narg('after_id')::uuid IS NULL OR operation_id > sqlc.narg('after_id')::uuid)
ORDER BY operation_id
LIMIT sqlc.arg('page_size');

-- name: PurgeTerminalOperations :execrows
-- Bounded batch (10k). Worker loops until result is 0. Uses
-- idx_operations_terminal_done_at (added in migration 008) so the planner
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
WHERE operation_id = $1 AND tenant_id = $2
  AND state IN ('PENDING', 'RUNNING');
