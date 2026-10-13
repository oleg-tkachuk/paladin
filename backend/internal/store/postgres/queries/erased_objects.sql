-- Tombstones for permanently deleted objects (migration 074).

-- HardDeleteObject writes them, in the statement that removes the row.

-- name: ListErasedObjects :many
-- Which of these ids were permanently deleted from this collection.
SELECT e.object_id
FROM erased_objects e
JOIN collections c ON c.id = e.collection_id
WHERE e.tenant_id = $1
  AND c.name = $2
  AND e.object_id = ANY(sqlc.arg('object_ids')::uuid[]);

-- name: PurgeErasedObjects :execrows
-- Bounded batch (10k), drained in a loop like PurgeTerminalOperations, whose
-- cutoff it shares: a tombstone serves a reissue of an operation, and is kept
-- exactly as long as that operation is.
DELETE FROM erased_objects
WHERE ctid IN (
    SELECT e.ctid FROM erased_objects AS e
    WHERE e.erased_at < $1
    ORDER BY e.erased_at
    LIMIT 10000
);
