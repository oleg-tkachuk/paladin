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

-- name: TouchOperation :execrows
-- Heartbeat for a RUNNING operation. Bumps updated_at and nothing else — in
-- particular it must not touch metadata, which carries the progress counters
-- an executor may be writing concurrently.
--
-- Liveness has to be separate from progress: an executor that reports no
-- progress (a short batch, or one iterating something without a total) would
-- otherwise look identical to a worker that died mid-operation, and
-- ReclaimStaleOperations would fail it while it was still working.
UPDATE operations
SET updated_at = now()
WHERE id = $1 AND state = 'RUNNING';

-- name: ReclaimStaleOperations :execrows
-- Fails operations left RUNNING by a worker that went away.
--
-- ClaimNext only ever selects PENDING, and there is no lease to expire, so a
-- row whose worker died between the claim and the terminal write is invisible
-- to every worker forever: not retried, not reaped (PurgeTerminalOperations
-- takes only terminal states), and shown to the operator as "running" for as
-- long as the database keeps it. One sat that way for two days.
--
-- Marked FAILED rather than requeued to PENDING, deliberately. The executors
-- here are not transactional across their items — a batch copy or delete may
-- have applied to half the set — so re-running one would repeat side effects
-- nobody can see. FAILED with WORKER_LOST tells the caller the truth: the
-- outcome is unknown, decide for yourself whether to reissue.
-- The response carries how far the executor got, when that is knowable.
--
-- "The outcome is unknown" is honest but coarse: a caller reissuing a batch
-- has no way to tell which items already landed. The runner's progress
-- reporter writes {processed, total} into metadata while an operation runs, so
-- for anything that reported progress the last snapshot is right there — the
-- difference between "unknown" and "stopped after 7 of 9". It is a lower
-- bound, not a count: progress is throttled to about one write a second, so
-- the executor may have finished more before it died.
--
-- metadata is not always progress — an operation that died before its first
-- report still holds the executor's arguments — hence the validity check
-- rather than a bare cast, which would fail the whole statement on one row.
UPDATE operations
SET state         = 'FAILED',
    error_code    = 'WORKER_LOST',
    error_message = 'the worker executing this operation stopped before it finished; '
                    'the work may have been partially applied',
    response      = convert_to(
        jsonb_build_object(
            'code', 'WORKER_LOST',
            'error', 'the worker executing this operation stopped before it finished; '
                     'the work may have been partially applied',
            'last_progress',
            CASE
                WHEN metadata IS NOT NULL
                 AND pg_input_is_valid(convert_from(metadata, 'UTF8'), 'jsonb')
                 AND (convert_from(metadata, 'UTF8')::jsonb ? 'processed')
                THEN convert_from(metadata, 'UTF8')::jsonb
                ELSE NULL
            END
        )::text, 'UTF8'),
    done_at       = now(),
    updated_at    = now()
WHERE state = 'RUNNING'
  AND updated_at < now() - (sqlc.arg('stale_after_micros')::bigint || ' microseconds')::interval;
