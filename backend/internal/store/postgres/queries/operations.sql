-- Long-running operation queries.

-- name: CreateOperation :exec
INSERT INTO operations (id, tenant_id, type, state, metadata, initiator_subject)
VALUES ($1, $2, $3, $4, $5, $6);

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
--
-- metadata is not written here: it holds the executor's arguments, set once at
-- creation. Progress has its own column and query (ReportOperationProgress).
UPDATE operations
SET state         = sqlc.arg('state')::operation_state,
    response      = COALESCE(sqlc.narg('response'),      response),
    error_code    = COALESCE(sqlc.narg('error_code'),    error_code),
    error_message = COALESCE(sqlc.narg('error_message'), error_message),
    -- The objects a batch's result says it could not act on (096); 0 for
    -- every other transition.
    failed_count  = sqlc.arg('failed_count')::bigint,
    done_at       = CASE WHEN sqlc.arg('state')::text IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
                         THEN now() ELSE done_at END,
    updated_at    = now()
-- A finished operation stays finished: a cancel, or the stale reclaimer's
-- FAILED, is not overwritten by the runner's late progress or result.
WHERE id = $1
  AND state IN ('PENDING', 'RUNNING');

-- name: ListOperations :many
-- Oldest first. The cursor compares `>`, so paging walks forward in time.
SELECT sqlc.embed(operations)
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('state')::operation_state IS NULL OR state = sqlc.narg('state')::operation_state)
  AND (sqlc.narg('after_id')::uuid IS NULL OR id > sqlc.narg('after_id')::uuid)
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  -- One user's own operations (087); NULL lists everyone's in the tenant.
  AND (sqlc.narg('initiator_subject')::text IS NULL OR initiator_subject = sqlc.narg('initiator_subject')::text)
  -- The ones their initiator cleared from their own list (088), when asked.
  AND (NOT sqlc.arg('hide_dismissed')::boolean OR dismissed_at IS NULL)
  AND (sqlc.narg('type_in')::text[] IS NULL OR type = ANY(sqlc.narg('type_in')::text[]))
  AND (sqlc.narg('type_like')::text IS NULL OR type LIKE sqlc.narg('type_like')::text)
  AND (sqlc.narg('error_code_in')::text[] IS NULL OR error_code = ANY(sqlc.narg('error_code_in')::text[]))
  -- The filter's `state == "…"` compares as text, not as a cast to the enum:
  -- a literal the enum does not hold then matches no row, as the CEL pass
  -- would decide, instead of failing the whole query.
  AND (sqlc.narg('state_in')::text[] IS NULL OR state::text = ANY(sqlc.narg('state_in')::text[]))
  -- `done` is what the domain derives from done_at (operationh.handler).
  AND (sqlc.narg('done')::bool IS NULL OR (done_at IS NOT NULL) = sqlc.narg('done')::bool)
  -- `has_failures`, as operationh derives it: failed, or a batch that left
  -- objects behind (096).
  AND (sqlc.narg('has_failures')::bool IS NULL
       OR (state = 'FAILED' OR failed_count > 0) = sqlc.narg('has_failures')::bool)
  AND (sqlc.narg('error_message_neq')::text IS NULL
       OR coalesce(error_message, '') <> sqlc.narg('error_message_neq')::text)
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR created_at <= sqlc.narg('created_at_lte')::timestamptz)
ORDER BY id
LIMIT sqlc.arg('page_size');

-- name: ListOperationsDesc :many
-- Newest first, for a caller showing current activity. A separate query
-- rather than a CASE in the ORDER BY: the cursor comparison has to flip with
-- the sort (`<` here, `>` above) or the second page walks away from the rows
-- the caller asked for, and sqlc cannot parameterise either.
SELECT sqlc.embed(operations)
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('state')::operation_state IS NULL OR state = sqlc.narg('state')::operation_state)
  AND (sqlc.narg('after_id')::uuid IS NULL OR id < sqlc.narg('after_id')::uuid)
  -- One user's own operations (087); NULL lists everyone's in the tenant.
  AND (sqlc.narg('initiator_subject')::text IS NULL OR initiator_subject = sqlc.narg('initiator_subject')::text)
  -- The ones their initiator cleared from their own list (088), when asked.
  AND (NOT sqlc.arg('hide_dismissed')::boolean OR dismissed_at IS NULL)
  AND (sqlc.narg('type_in')::text[] IS NULL OR type = ANY(sqlc.narg('type_in')::text[]))
  AND (sqlc.narg('type_like')::text IS NULL OR type LIKE sqlc.narg('type_like')::text)
  AND (sqlc.narg('error_code_in')::text[] IS NULL OR error_code = ANY(sqlc.narg('error_code_in')::text[]))
  -- The filter's `state == "…"` compares as text, not as a cast to the enum:
  -- a literal the enum does not hold then matches no row, as the CEL pass
  -- would decide, instead of failing the whole query.
  AND (sqlc.narg('state_in')::text[] IS NULL OR state::text = ANY(sqlc.narg('state_in')::text[]))
  -- `done` is what the domain derives from done_at (operationh.handler).
  AND (sqlc.narg('done')::bool IS NULL OR (done_at IS NOT NULL) = sqlc.narg('done')::bool)
  -- `has_failures`, as operationh derives it: failed, or a batch that left
  -- objects behind (096).
  AND (sqlc.narg('has_failures')::bool IS NULL
       OR (state = 'FAILED' OR failed_count > 0) = sqlc.narg('has_failures')::bool)
  AND (sqlc.narg('error_message_neq')::text IS NULL
       OR coalesce(error_message, '') <> sqlc.narg('error_message_neq')::text)
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR created_at <= sqlc.narg('created_at_lte')::timestamptz)
ORDER BY id DESC
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

-- name: DismissOperations :execrows
-- Clears finished operations from their initiator's own list: only that
-- subject's, only those that have finished, and only once. ids narrows it to
-- those operations; NULL clears every finished one.
UPDATE operations
SET dismissed_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')::uuid
  AND initiator_subject = sqlc.arg('initiator_subject')::text
  AND state IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
  AND dismissed_at IS NULL
  AND (sqlc.narg('ids')::uuid[] IS NULL OR id = ANY (sqlc.narg('ids')::uuid[]));

-- name: ReportOperationProgress :execrows
-- Records a RUNNING operation's latest {processed, total} snapshot. Its own
-- column, so the arguments in metadata survive the whole run — the stale
-- reclaimer used to fail an operation whose metadata had been overwritten by
-- progress, and the row no longer said what it had been for.
UPDATE operations
SET progress   = sqlc.arg('progress')::jsonb,
    updated_at = now()
WHERE id = $1 AND state = 'RUNNING';

-- name: TouchOperation :execrows
-- Heartbeat for a RUNNING operation. Bumps updated_at and nothing else — in
-- particular it must not touch progress, which an executor may be writing
-- concurrently.
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
-- Reissuing the same batch is the recovery, and it is safe: each executor
-- counts an item the dead run already applied as succeeded (already_applied in
-- its response) rather than repeating or failing it — a permanent delete by
-- the tombstones its erasures left (erased_objects) — and a copy adopts the
-- PENDING destination a dead run left half done (objects.created_by_operation).
--
-- For a caller that wants to know before reissuing, the runner's progress
-- reporter writes {processed, total} into progress while an operation runs, so
-- for anything that reported progress the last snapshot is right there — the
-- difference between "unknown" and "stopped after 7 of 9". It is a lower
-- bound, not a count: progress is throttled to about one write a second, so
-- the executor may have finished more before it died. An operation that died
-- before its first report has no progress, and last_progress is null.
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
            'last_progress', progress
        )::text, 'UTF8'),
    done_at       = now(),
    updated_at    = now()
WHERE state = 'RUNNING'
  AND updated_at < now() - (sqlc.arg('stale_after_micros')::bigint || ' microseconds')::interval;

-- name: OperationIsLive :one
-- Whether an operation may still be working: PENDING or RUNNING. A purged
-- operation is not live — only terminal ones are purged.
SELECT EXISTS (
    SELECT 1 FROM operations
    WHERE id = $1 AND state IN ('PENDING', 'RUNNING')
);

-- name: CountOperationsByState :many
-- How many operations of a listing are in each state: ListOperations' own
-- predicates, without its cursor, grouped. The caller leaves out the state
-- facet's own choices, so each count says what choosing that state gives.
SELECT state::text AS value, count(*)::bigint AS count
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('initiator_subject')::text IS NULL OR initiator_subject = sqlc.narg('initiator_subject')::text)
  AND (NOT sqlc.arg('hide_dismissed')::boolean OR dismissed_at IS NULL)
  AND (sqlc.narg('type_in')::text[] IS NULL OR type = ANY(sqlc.narg('type_in')::text[]))
  AND (sqlc.narg('type_like')::text IS NULL OR type LIKE sqlc.narg('type_like')::text)
  AND (sqlc.narg('error_code_in')::text[] IS NULL OR error_code = ANY(sqlc.narg('error_code_in')::text[]))
  AND (sqlc.narg('state_in')::text[] IS NULL OR state::text = ANY(sqlc.narg('state_in')::text[]))
  AND (sqlc.narg('done')::bool IS NULL OR (done_at IS NOT NULL) = sqlc.narg('done')::bool)
  AND (sqlc.narg('has_failures')::bool IS NULL
       OR (state = 'FAILED' OR failed_count > 0) = sqlc.narg('has_failures')::bool)
  AND (sqlc.narg('error_message_neq')::text IS NULL
       OR coalesce(error_message, '') <> sqlc.narg('error_message_neq')::text)
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR created_at <= sqlc.narg('created_at_lte')::timestamptz)
GROUP BY state;

-- name: CountOperationsByType :many
-- As CountOperationsByState, by type.
SELECT type AS value, count(*)::bigint AS count
FROM operations
WHERE tenant_id = $1
  AND (sqlc.narg('initiator_subject')::text IS NULL OR initiator_subject = sqlc.narg('initiator_subject')::text)
  AND (NOT sqlc.arg('hide_dismissed')::boolean OR dismissed_at IS NULL)
  AND (sqlc.narg('type_in')::text[] IS NULL OR type = ANY(sqlc.narg('type_in')::text[]))
  AND (sqlc.narg('type_like')::text IS NULL OR type LIKE sqlc.narg('type_like')::text)
  AND (sqlc.narg('error_code_in')::text[] IS NULL OR error_code = ANY(sqlc.narg('error_code_in')::text[]))
  AND (sqlc.narg('state_in')::text[] IS NULL OR state::text = ANY(sqlc.narg('state_in')::text[]))
  AND (sqlc.narg('done')::bool IS NULL OR (done_at IS NOT NULL) = sqlc.narg('done')::bool)
  AND (sqlc.narg('has_failures')::bool IS NULL
       OR (state = 'FAILED' OR failed_count > 0) = sqlc.narg('has_failures')::bool)
  AND (sqlc.narg('error_message_neq')::text IS NULL
       OR coalesce(error_message, '') <> sqlc.narg('error_message_neq')::text)
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR created_at <= sqlc.narg('created_at_lte')::timestamptz)
GROUP BY type;
