-- Purge debt: the retry handle for bytes whose DB row is already gone.
-- See ADR-0017 and migrations/001_initial_schema.sql: storage_path is
-- denormalised here because the object row is gone before the purge runs.

-- name: InsertPendingPurge :exec
INSERT INTO pending_purges (
    id, tenant_id, object_id, bucket_id, collection_name, path
) VALUES ($1, $2, $3,
          (SELECT b.id FROM buckets b
             JOIN storage_backends sb ON sb.id = b.backend_id
            WHERE sb.name = $4 AND b.name = $5),
          $6, $7);

-- name: DeletePendingPurge :execrows
DELETE FROM pending_purges WHERE id = $1;

-- ListDuePurges claims work for one drainer tick. FOR UPDATE SKIP LOCKED so
-- concurrent worker replicas divide the backlog instead of colliding on it —
-- the same claim discipline the event-delivery outbox uses.
-- name: ListDuePurges :many
-- Backend and bucket come back by NAME: the storage adapter addresses S3 by
-- name, and the bucket still exists even though the object row does not.
-- Each row carries its bucket's id and the state the bucket gate decides on
-- (bucketgate.State): the drainer asks the gate in Go and claims again without
-- the buckets it refused, whose debt stays due, untouched, for a tick whose
-- gate admits it.
SELECT p.id, p.tenant_id, p.object_id, p.collection_name, p.path, p.attempts,
       sb.name AS backend_name,
       b.name  AS bucket_name,
       b.id    AS bucket_id,
       sb.enabled, sb.read_only, b.provision_state
  FROM pending_purges p
  JOIN buckets b           ON b.id = p.bucket_id
  JOIN storage_backends sb ON sb.id = b.backend_id
 WHERE p.next_attempt_at <= now()
   AND NOT (p.bucket_id = ANY(coalesce(sqlc.arg('deferred_bucket_ids')::uuid[], '{}')))
 ORDER BY p.next_attempt_at
 LIMIT $1
 FOR UPDATE OF p SKIP LOCKED;

-- ReschedulePendingPurge records a failed attempt and pushes the row out by
-- the caller-computed backoff. Attempts is bumped here rather than in the
-- worker so a crash between the storage call and this update cannot lose the
-- count.
-- name: ReschedulePendingPurge :exec
UPDATE pending_purges
   SET attempts        = attempts + 1,
       last_error      = $2,
       next_attempt_at = now() + $3::interval
 WHERE id = $1;
