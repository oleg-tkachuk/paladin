-- Object AI markers — drives the on-store summarization + embedding
-- workers. Both queries are batch-bounded so a long-overdue first run
-- doesn't pin connections; workers loop on a ticker.

-- name: ListObjectsForSummary :many
-- Returns AVAILABLE objects without a summary, oldest first. Caller
-- joins object_keys → bucket inline so the worker can call S3 in a
-- single round-trip per item.
SELECT o.object_id, o.tenant_id, o.object_key, o.key,
       o.content_type, o.size_bytes,
       k.backend_id, k.bucket_name
FROM objects o
JOIN object_keys k
  ON k.tenant_id = o.tenant_id AND k.object_key = o.object_key
WHERE o.state = 'AVAILABLE' AND o.summary IS NULL
ORDER BY o.object_id
LIMIT sqlc.arg('batch_size');

-- name: SetObjectSummary :execrows
-- No OCC. Workers run one-at-a-time per object (batch dedupe is by the
-- partial index above) and the column is write-once-then-stable; a lost
-- update would only re-run the summarizer.
UPDATE objects
SET summary = sqlc.arg('summary')
WHERE object_id = sqlc.arg('object_id')
  AND summary IS NULL;

-- name: ListObjectsForEmbedding :many
-- Returns AVAILABLE objects with a summary but no embedding marker.
-- Embedding rides on the summary text (much smaller payload than the
-- raw object body) — keeps day-1 simple. Future: chunk-level embedding
-- of the body for objects above a configurable size threshold.
SELECT o.object_id, o.tenant_id, o.object_key, o.key,
       o.summary, o.content_type,
       k.backend_id, k.bucket_name
FROM objects o
JOIN object_keys k
  ON k.tenant_id = o.tenant_id AND k.object_key = o.object_key
WHERE o.state = 'AVAILABLE'
  AND o.summary IS NOT NULL
  AND o.embedding_indexed_at IS NULL
ORDER BY o.object_id
LIMIT sqlc.arg('batch_size');

-- name: MarkObjectEmbedded :execrows
UPDATE objects
SET embedding_indexed_at = now()
WHERE object_id = sqlc.arg('object_id')
  AND embedding_indexed_at IS NULL;
