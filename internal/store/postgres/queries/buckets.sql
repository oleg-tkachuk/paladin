-- Bucket queries. A bucket is a physical S3 bucket inside a storage backend.
-- Created lazily via BucketService.CreateBucket; ObjectKey rows FK to the
-- (backend_id, bucket_name) composite key.

-- name: CreateBucket :exec
INSERT INTO buckets (backend_id, bucket_name, display_name, region, labels)
VALUES ($1, $2, $3, $4, $5);

-- name: GetBucket :one
SELECT sqlc.embed(buckets)
FROM buckets
WHERE backend_id = $1 AND bucket_name = $2;

-- name: UpdateBucket :execrows
UPDATE buckets
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    labels       = COALESCE(sqlc.narg('labels'),       labels)
WHERE backend_id = $1 AND bucket_name = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: DeleteBucket :execrows
DELETE FROM buckets
WHERE backend_id = $1 AND bucket_name = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: ListBuckets :many
SELECT sqlc.embed(buckets)
FROM buckets
WHERE (sqlc.narg('backend_id')::text IS NULL OR backend_id = sqlc.narg('backend_id')::text)
  AND (sqlc.narg('after_name')::text IS NULL
       OR (backend_id, bucket_name) > (sqlc.narg('after_backend_id')::text, sqlc.narg('after_name')::text))
ORDER BY backend_id, bucket_name
LIMIT sqlc.arg('page_size');

-- name: CountObjectKeysReferencingBucket :one
SELECT count(*)::bigint AS count
FROM object_keys
WHERE backend_id = $1 AND bucket_name = $2;
