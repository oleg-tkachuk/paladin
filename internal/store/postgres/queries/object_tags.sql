-- Object tag queries. Tenant-scoped; addressed by (tenant_id, slug).

-- name: CreateObjectTag :exec
INSERT INTO object_tags (tenant_id, slug, display_name, description, labels)
VALUES ($1, $2, $3, $4, $5);

-- name: GetObjectTag :one
SELECT sqlc.embed(object_tags)
FROM object_tags
WHERE tenant_id = $1 AND slug = $2;

-- name: UpdateObjectTag :execrows
UPDATE object_tags
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    description  = COALESCE(sqlc.narg('description'),  description),
    labels       = COALESCE(sqlc.narg('labels'),       labels)
WHERE tenant_id = $1 AND slug = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: ListObjectTags :many
SELECT sqlc.embed(object_tags)
FROM object_tags
WHERE tenant_id = $1
  AND (sqlc.narg('after_slug')::text IS NULL OR slug > sqlc.narg('after_slug')::text)
ORDER BY slug
LIMIT sqlc.arg('page_size');

-- name: DeleteObjectTag :execrows
DELETE FROM object_tags
WHERE tenant_id = $1 AND slug = $2
  AND resource_version = sqlc.arg('expected_version');
