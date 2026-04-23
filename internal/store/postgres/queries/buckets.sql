-- Bucket queries.

-- name: CreateBucket :exec
INSERT INTO buckets (
    tenant_id, bucket_id, display_name, storage_backend,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetBucket :one
SELECT sqlc.embed(buckets)
FROM buckets
WHERE tenant_id = $1 AND bucket_id = $2;

-- name: UpdateBucket :execrows
UPDATE buckets
SET display_name    = COALESCE(sqlc.narg('display_name'),    display_name),
    cedar_policy    = COALESCE(sqlc.narg('policy'),          cedar_policy),
    cedar_policy_hash = CASE WHEN sqlc.narg('policy') IS NULL
                             THEN cedar_policy_hash
                             ELSE sqlc.narg('policy_hash') END,
    lifecycle_rules = COALESCE(sqlc.narg('lifecycle_rules'), lifecycle_rules)
WHERE tenant_id = $1 AND bucket_id = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: ListBuckets :many
SELECT sqlc.embed(buckets)
FROM buckets
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL OR bucket_id > sqlc.narg('after_id')::text)
ORDER BY bucket_id
LIMIT sqlc.arg('page_size');

-- name: DeleteBucket :execrows
DELETE FROM buckets
WHERE tenant_id = $1 AND bucket_id = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: GetEffectivePolicy :one
-- Returns tenant-inherited policy concatenated with the bucket-specific policy.
-- Order is: tenant policies first, then bucket — Cedar treats them as a single
-- policy set; ordering only affects diagnostic output.
SELECT t.inherited_cedar_policy AS tenant_policy,
       t.inherited_policy_hash  AS tenant_hash,
       b.cedar_policy           AS bucket_policy,
       b.cedar_policy_hash      AS bucket_hash
FROM tenants t
LEFT JOIN buckets b
  ON b.tenant_id = t.tenant_id
 AND b.bucket_id = sqlc.narg('bucket_id')::text
WHERE t.tenant_id = $1;
