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
