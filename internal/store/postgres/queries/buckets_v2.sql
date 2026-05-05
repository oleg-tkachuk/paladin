-- v2 bucket queries — full surface for admin/v1.BucketService.

-- name: GetBucketV2 :one
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       resource_version, created_at, updated_at
FROM buckets
WHERE backend_id = $1 AND bucket_name = $2;

-- name: CreateBucketV2 :exec
INSERT INTO buckets (
    backend_id, bucket_name, display_name, region, labels,
    owner_tenant_id, cedar_policy, constraints
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListBucketsV2 :many
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       resource_version, created_at, updated_at
FROM buckets
WHERE (sqlc.narg('backend_id')::text IS NULL OR backend_id = sqlc.narg('backend_id')::text)
  AND (backend_id, bucket_name) > (sqlc.arg('after_backend_id')::text, sqlc.arg('after_name')::text)
ORDER BY backend_id, bucket_name
LIMIT sqlc.arg('page_size');

-- name: ListAccessibleBuckets :many
-- Returns shared buckets (owner IS NULL) plus buckets owned by the tenant.
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       resource_version, created_at, updated_at
FROM buckets
WHERE (owner_tenant_id IS NULL OR owner_tenant_id = $1)
  AND (backend_id, bucket_name) > ($2::text, $3::text)
ORDER BY backend_id, bucket_name
LIMIT $4;

-- name: SetBucketPolicy :execrows
UPDATE buckets
SET cedar_policy      = $3,
    cedar_policy_hash = NULL
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketLifecycle :execrows
UPDATE buckets
SET lifecycle_rules = $3
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketObjectLock :execrows
UPDATE buckets
SET object_lock_enabled                   = $3,
    object_lock_default_mode              = $4,
    object_lock_default_retention_seconds = $5
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketVersioning :execrows
UPDATE buckets
SET versioning_enabled                = $3,
    versioning_keep_deletes_forever   = $4
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketReplication :execrows
UPDATE buckets
SET replication_enabled     = $3,
    replication_destination = $4,
    replication_filter      = $5
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketConstraints :execrows
UPDATE buckets
SET constraints = $3
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: UpdateBucketBasic :execrows
UPDATE buckets
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    labels       = COALESCE(sqlc.narg('labels'), labels),
    owner_tenant_id = COALESCE(sqlc.narg('owner_tenant_id'), owner_tenant_id)
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteBucketV2 :execrows
DELETE FROM buckets
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);
