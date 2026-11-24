-- name: GetReplicationWatermark :one
SELECT watermark
FROM replication_state
WHERE bucket_id = (SELECT b.id FROM buckets b
          JOIN storage_backends sb ON sb.id = b.backend_id
         WHERE sb.name = $1 AND b.name = $2);

-- name: UpsertReplicationWatermark :exec
-- Monotonic upsert: never moves the watermark backwards. Concurrent
-- replicas may try to advance with stale values; the GREATEST() guard
-- preserves the highest seen committed_at.
INSERT INTO replication_state (bucket_id, watermark)
VALUES ((SELECT b.id FROM buckets b
          JOIN storage_backends sb ON sb.id = b.backend_id
         WHERE sb.name = $1 AND b.name = $2), $3)
ON CONFLICT (bucket_id) DO UPDATE
SET watermark  = GREATEST(replication_state.watermark, EXCLUDED.watermark),
    updated_at = now();

-- name: ReplicationClock :one
-- The database's clock, which stamps objects.committed_at: the replication
-- worker's horizon is measured on it, so no clock skew between a worker pod
-- and the database moves the watermark past rows not yet stamped.
SELECT now()::timestamptz AS now;

-- name: GetObjectReplicationState :one
-- One object's state and resource version, re-read by the replication worker
-- before it passes over an object whose bytes the source does not hold.
SELECT state, resource_version
FROM objects
WHERE tenant_id = $1 AND id = $2;
