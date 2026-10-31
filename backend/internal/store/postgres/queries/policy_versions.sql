-- Policy history (migration 101). Rows are written by triggers only; the
-- application reads them.

-- name: ListPolicyVersions :many
-- A layer's versions, newest first. before_version 0 starts at the newest;
-- otherwise the page holds the versions below it.
SELECT version, cedar_policy, written_by, written_by_tenant_id, written_at
  FROM policy_versions
 WHERE layer = sqlc.arg('layer')::policy_layer
   AND owner_id = sqlc.arg('owner_id')
   AND (sqlc.arg('before_version')::integer = 0 OR version < sqlc.arg('before_version')::integer)
 ORDER BY version DESC
 LIMIT sqlc.arg('page_size');

-- name: CollectionIDForPolicy :one
-- The collection whose policy layer the name tenants/{tenant}/collections/{name} addresses.
SELECT id FROM collections WHERE tenant_id = $1 AND name = $2;

-- name: BackendIDForPolicy :one
-- The storage backend whose policy layer storageBackends/{backend} addresses,
-- by its name: the admin API's id for it.
SELECT id FROM storage_backends WHERE name = $1;

-- name: BucketIDForPolicy :one
-- The bucket whose policy layer storageBackends/{backend}/buckets/{name}
-- addresses, its backend by name.
SELECT b.id
  FROM buckets b
  JOIN storage_backends s ON s.id = b.backend_id
 WHERE s.name = sqlc.arg('backend') AND b.name = sqlc.arg('bucket');
