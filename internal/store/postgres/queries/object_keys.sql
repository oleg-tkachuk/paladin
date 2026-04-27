-- ObjectKey queries.

-- name: CreateObjectKey :exec
INSERT INTO object_keys (
    tenant_id, object_key, display_name, backend_id, bucket_name,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetObjectKey :one
SELECT sqlc.embed(object_keys)
FROM object_keys
WHERE tenant_id = $1 AND object_key = $2;

-- name: UpdateObjectKey :execrows
-- expected_version=0 disables the OCC guard (force update).
UPDATE object_keys
SET display_name    = COALESCE(sqlc.narg('display_name'),    display_name),
    cedar_policy    = COALESCE(sqlc.narg('policy'),          cedar_policy),
    cedar_policy_hash = CASE WHEN sqlc.narg('policy') IS NULL
                             THEN cedar_policy_hash
                             ELSE sqlc.narg('policy_hash') END,
    lifecycle_rules = COALESCE(sqlc.narg('lifecycle_rules'), lifecycle_rules)
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListObjectKeys :many
SELECT sqlc.embed(object_keys)
FROM object_keys
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL OR object_key > sqlc.narg('after_id')::text)
ORDER BY object_key
LIMIT sqlc.arg('page_size');

-- name: DeleteObjectKey :execrows
DELETE FROM object_keys
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: GetEffectivePolicy :one
-- Returns tenant-inherited policy concatenated with the object_key-specific policy.
-- Order is: tenant policies first, then object_key — Cedar treats them as a single
-- policy set; ordering only affects diagnostic output.
SELECT t.inherited_cedar_policy AS tenant_policy,
       t.inherited_policy_hash  AS tenant_hash,
       b.cedar_policy           AS bucket_policy,
       b.cedar_policy_hash      AS bucket_hash
FROM tenants t
LEFT JOIN object_keys b
  ON b.tenant_id = t.tenant_id
 AND b.object_key = sqlc.narg('object_key')::text
WHERE t.tenant_id = $1;
