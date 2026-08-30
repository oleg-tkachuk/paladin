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
SELECT sqlc.embed(b), sb.name AS backend_name
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE (sqlc.narg('backend_id')::text IS NULL OR sb.name = sqlc.narg('backend_id')::text)
  AND (sqlc.narg('after_name')::text IS NULL
       OR (sb.name, b.name) > (sqlc.narg('after_backend_id')::text, sqlc.narg('after_name')::text))
ORDER BY sb.name, b.name
LIMIT sqlc.arg('page_size');

-- name: CountBucketReferences :many
--
-- Every relation that holds a bucket under ON DELETE RESTRICT, counted in one
-- round trip. The list is not a guess: it is the RESTRICT set as the schema
-- declares it, and TestBucketReferenceListMatchesSchema (integration) reads
-- pg_constraint and fails if the two ever diverge. CASCADE dependents
-- (replication_state, quotas) are deliberately absent — they do not block a
-- delete, so naming them would only send an operator after rows that will
-- clean themselves up.
--
-- Rows come back for relations with a zero count too; the caller decides what
-- to do with those. tenant_storage_migrations carries two of the constraints
-- (source and target), so it is matched on both columns and reported once.
WITH target AS (
    SELECT b.id
    FROM buckets b
    JOIN storage_backends sb ON sb.id = b.backend_id
    WHERE sb.name = $1 AND b.name = $2
)
SELECT 'collections'::text AS relation, count(*)::bigint AS count
    FROM collections WHERE bucket_id IN (SELECT id FROM target)
UNION ALL
SELECT 'tenant_default_bindings'::text, count(*)::bigint
    FROM tenant_default_bindings WHERE bucket_id IN (SELECT id FROM target)
UNION ALL
SELECT 'tenant_storage_migrations'::text, count(*)::bigint
    FROM tenant_storage_migrations
    WHERE source_bucket_id IN (SELECT id FROM target)
       OR target_bucket_id IN (SELECT id FROM target)
UNION ALL
SELECT 'multipart_uploads'::text, count(*)::bigint
    FROM multipart_uploads WHERE bucket_id IN (SELECT id FROM target)
UNION ALL
SELECT 'pending_purges'::text, count(*)::bigint
    FROM pending_purges WHERE bucket_id IN (SELECT id FROM target)
UNION ALL
SELECT 'pending_multipart_aborts'::text, count(*)::bigint
    FROM pending_multipart_aborts WHERE bucket_id IN (SELECT id FROM target);
