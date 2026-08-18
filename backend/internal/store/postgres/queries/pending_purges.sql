-- Purge debt: the retry handle for bytes whose DB row is already gone.
-- See migrations/069_pending_purges.sql for why this table exists.

-- name: InsertPendingPurge :exec
INSERT INTO pending_purges (
    purge_id, tenant_id, object_id, backend_id, bucket_name, object_key, key
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: DeletePendingPurge :execrows
DELETE FROM pending_purges WHERE purge_id = $1;

-- ListDuePurges claims work for one drainer tick. FOR UPDATE SKIP LOCKED so
-- concurrent worker replicas divide the backlog instead of colliding on it —
-- the same claim discipline the event-delivery outbox uses.
-- name: ListDuePurges :many
SELECT purge_id, tenant_id, object_id, backend_id, bucket_name, object_key, key, attempts
  FROM pending_purges
 WHERE next_attempt_at <= now()
 ORDER BY next_attempt_at
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- ReschedulePendingPurge records a failed attempt and pushes the row out by
-- the caller-computed backoff. Attempts is bumped here rather than in the
-- worker so a crash between the storage call and this update cannot lose the
-- count.
-- name: ReschedulePendingPurge :exec
UPDATE pending_purges
   SET attempts        = attempts + 1,
       last_error      = $2,
       next_attempt_at = now() + $3::interval
 WHERE purge_id = $1;

-- name: CountPendingPurges :one
SELECT count(*) FROM pending_purges;
