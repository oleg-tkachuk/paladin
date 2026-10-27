-- Policy history (migration 101). Rows are written by triggers only; the
-- application reads them.

-- name: ListPolicyVersions :many
-- A layer's versions, newest first. before_version 0 starts at the newest;
-- otherwise the page holds the versions below it.
SELECT version, cedar_policy, written_by, written_at
  FROM policy_versions
 WHERE layer = sqlc.arg('layer')::policy_layer
   AND owner_id = sqlc.arg('owner_id')
   AND (sqlc.arg('before_version')::integer = 0 OR version < sqlc.arg('before_version')::integer)
 ORDER BY version DESC
 LIMIT sqlc.arg('page_size');

-- name: CollectionIDForPolicy :one
-- The collection whose policy layer the name tenants/{tenant}/collections/{name} addresses.
SELECT id FROM collections WHERE tenant_id = $1 AND name = $2;

-- name: BucketIDForPolicy :one
-- The bucket whose policy layer storageBackends/{backend}/buckets/{name} addresses.
SELECT id FROM buckets WHERE backend_id = $1 AND name = $2;
