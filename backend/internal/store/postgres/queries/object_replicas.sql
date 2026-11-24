-- Per-object replication state (migration 121, ADR-0050). Every query here
-- runs on the worker's cross-tenant connection.

-- name: SeedReplication :execrows
-- Queues every AVAILABLE object whose bytes are in the bucket
-- (objects.placement_bucket_id, ADR-0051), once: the
-- replication_seeds row is written by the same statement, and a bucket that
-- has one is left alone. The triggers queue every write from then on; a
-- change to the bucket's replication settings drops the seed row, so the
-- bucket is queued again. Returns how many rows it queued.
WITH marked AS (
    INSERT INTO replication_seeds (bucket_id)
    VALUES (sqlc.arg('bucket_id'))
    ON CONFLICT (bucket_id) DO NOTHING
    RETURNING bucket_id
)
INSERT INTO object_replicas
    (source_bucket_id, tenant_id, collection_name, path, object_id, next_attempt_at)
SELECT m.bucket_id, o.tenant_id, c.name, o.path, o.id, now()
  FROM marked m
  JOIN collections c ON TRUE
  JOIN objects o     ON o.tenant_id = c.tenant_id AND o.collection_id = c.id
 WHERE o.placement_bucket_id = m.bucket_id
   AND o.state = 'AVAILABLE'
ON CONFLICT (source_bucket_id, tenant_id, collection_name, path) DO UPDATE
   SET object_id       = EXCLUDED.object_id,
       generation      = object_replicas.generation + 1,
       next_attempt_at = now(),
       attempts        = 0,
       last_error      = NULL,
       updated_at      = now();

-- name: ClaimReplicas :many
-- Claims up to limit of a bucket's due rows for one worker, oldest first.
-- FOR UPDATE SKIP LOCKED and the claim's lease keep concurrent worker
-- replicas on disjoint rows; a write that marks a claimed row due again
-- leaves the lease alone, so one worker handles a location at a time. A
-- tenant in the trash is frozen: its rows wait for a restore or a purge.
WITH due AS (
    SELECT r.source_bucket_id, r.tenant_id, r.collection_name, r.path
      FROM object_replicas r
     WHERE r.source_bucket_id = sqlc.arg('bucket_id')
       AND r.next_attempt_at <= now()
       AND (r.claimed_until IS NULL OR r.claimed_until <= now())
       AND NOT EXISTS (SELECT 1 FROM tenants t
                        WHERE t.id = r.tenant_id AND t.deleted_at IS NOT NULL)
     ORDER BY r.next_attempt_at
     LIMIT sqlc.arg('claim_limit')
       FOR UPDATE OF r SKIP LOCKED
)
UPDATE object_replicas r
   SET claimed_until = now() + sqlc.arg('lease')::interval
  FROM due
 WHERE r.source_bucket_id = due.source_bucket_id
   AND r.tenant_id = due.tenant_id
   AND r.collection_name = due.collection_name
   AND r.path = due.path
RETURNING r.tenant_id, r.collection_name, r.path, r.object_id, r.generation, r.attempts,
          r.replica_bucket_id, r.replica_object_id, r.replica_written_at;

-- name: GetReplicaObjects :many
-- The claimed rows' objects as they are now, with the bucket each one's
-- bytes are in (objects.placement_bucket_id, ADR-0051; every row the queue
-- names was AVAILABLE, so is placed) and the byte
-- write a replica of it must match:
-- bytes_written_at, which every promote sets, or for a row promoted before
-- migration 111 its first commit. Every column cel.ObjectVars projects, so
-- the bucket's filter sees what a ListObjects filter sees.
SELECT o.id, o.state, o.placement_bucket_id AS bucket_id, o.path, o.content_type, o.size_bytes, o.external_ref,
       o.metadata, o.tags, o.created_at, o.updated_at, o.committed_at, o.resource_version,
       COALESCE(o.bytes_written_at, o.committed_at, o.created_at)::timestamptz AS written_at
  FROM objects o
 WHERE o.id = ANY(sqlc.arg('ids')::uuid[]);

-- name: RecordReplicaCopy :exec
-- Records the replica a copy wrote, and settles the row unless a write
-- marked it due again since the claim (generation): that write is copied by
-- a later pass.
UPDATE object_replicas
   SET replica_bucket_id  = sqlc.arg('replica_bucket_id'),
       replica_object_id  = sqlc.arg('replica_object_id'),
       replica_written_at = sqlc.arg('replica_written_at'),
       replica_size_bytes = sqlc.arg('replica_size_bytes'),
       next_attempt_at    = CASE WHEN generation = sqlc.arg('generation') THEN NULL ELSE next_attempt_at END,
       attempts           = CASE WHEN generation = sqlc.arg('generation') THEN 0 ELSE attempts END,
       last_error         = CASE WHEN generation = sqlc.arg('generation') THEN NULL ELSE last_error END,
       claimed_until      = NULL,
       updated_at         = now()
 WHERE source_bucket_id = sqlc.arg('bucket_id') AND tenant_id = sqlc.arg('tenant_id')
   AND collection_name = sqlc.arg('collection_name') AND path = sqlc.arg('path');

-- name: SettleReplica :exec
-- Settles a row the worker decided without writing a replica — filtered
-- out, already current, its object trashed, its source confirmed missing —
-- unless a write marked it due again since the claim.
UPDATE object_replicas
   SET next_attempt_at = CASE WHEN generation = sqlc.arg('generation') THEN NULL ELSE next_attempt_at END,
       attempts        = CASE WHEN generation = sqlc.arg('generation') THEN 0 ELSE attempts END,
       last_error      = CASE WHEN generation = sqlc.arg('generation') THEN NULL ELSE last_error END,
       claimed_until   = NULL,
       updated_at      = now()
 WHERE source_bucket_id = sqlc.arg('bucket_id') AND tenant_id = sqlc.arg('tenant_id')
   AND collection_name = sqlc.arg('collection_name') AND path = sqlc.arg('path');

-- name: RetryReplica :exec
-- Records a failed attempt and puts the row off by the worker's backoff,
-- unless a write marked it due again since the claim: that write is tried
-- afresh.
UPDATE object_replicas
   SET attempts        = CASE WHEN generation = sqlc.arg('generation') THEN attempts + 1 ELSE attempts END,
       last_error      = CASE WHEN generation = sqlc.arg('generation') THEN sqlc.arg('last_error')::text ELSE last_error END,
       next_attempt_at = CASE WHEN generation = sqlc.arg('generation')
                              THEN now() + sqlc.arg('delay')::interval
                              ELSE next_attempt_at END,
       claimed_until   = NULL,
       updated_at      = now()
 WHERE source_bucket_id = sqlc.arg('bucket_id') AND tenant_id = sqlc.arg('tenant_id')
   AND collection_name = sqlc.arg('collection_name') AND path = sqlc.arg('path');

-- name: ReleaseReplica :exec
-- Gives a claimed row back untouched, due as it was: a pass that stopped
-- before reaching it, or a dry run.
UPDATE object_replicas
   SET claimed_until = NULL
 WHERE source_bucket_id = sqlc.arg('bucket_id') AND tenant_id = sqlc.arg('tenant_id')
   AND collection_name = sqlc.arg('collection_name') AND path = sqlc.arg('path');

-- name: ForgetReplica :execrows
-- Drops the row of an object that is gone or left the bucket, unless a write
-- took the location since the claim: the row is then the new object's.
DELETE FROM object_replicas
 WHERE source_bucket_id = sqlc.arg('bucket_id') AND tenant_id = sqlc.arg('tenant_id')
   AND collection_name = sqlc.arg('collection_name') AND path = sqlc.arg('path')
   AND generation = sqlc.arg('generation');

-- name: GetObjectReplicationState :one
-- One object's state and resource version, re-read by the replication worker
-- before it passes over an object whose bytes the source does not hold.
SELECT state, resource_version
FROM objects
WHERE tenant_id = $1 AND id = $2;
