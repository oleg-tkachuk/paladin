-- Bucket queries. A bucket is a physical S3 bucket inside a storage backend.
-- Created lazily via BucketService.CreateBucket; Collection rows FK to the
-- bucket_id foreign key.

-- name: CreateBucket :exec
-- Idempotent: a duplicate (backend_id, name) is a no-op so that the
-- handler can return the existing row instead of erroring. The S3-side
-- CreateBucket is also idempotent (s3adapter swallows BucketAlreadyOwnedByYou),
-- so the API surface stays consistently retry-safe.
INSERT INTO buckets (backend_id, name, display_name, region, labels)
VALUES ((SELECT sb.id FROM storage_backends sb WHERE sb.name = $1),
        $2, $3, $4, $5)
ON CONFLICT (backend_id, name) DO NOTHING;

-- name: GetBucket :one
SELECT sqlc.embed(buckets),
       (SELECT sb.name FROM storage_backends sb WHERE sb.id = buckets.backend_id) AS backend_name
FROM buckets
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2);

-- name: UpdateBucket :execrows
-- expected_version=0 disables the OCC guard (force update).
UPDATE buckets
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    labels       = COALESCE(sqlc.narg('labels'),       labels)
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteBucket :execrows
DELETE FROM buckets
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListBuckets :many
SELECT sqlc.embed(buckets),
       (SELECT sb.name FROM storage_backends sb WHERE sb.id = buckets.backend_id) AS backend_name
FROM buckets
WHERE (sqlc.narg('backend_id')::text IS NULL OR backend_id = sqlc.narg('backend_id')::text)
  AND (sqlc.narg('after_name')::text IS NULL
       OR (backend_id, name) > (sqlc.narg('after_backend_id')::text, sqlc.narg('after_name')::text))
ORDER BY backend_id, name
LIMIT sqlc.arg('page_size');

-- name: CountCollectionsReferencingBucket :one
SELECT count(*)::bigint AS count
FROM collections c
JOIN buckets b           ON b.id = c.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = $1 AND b.name = $2;
