-- Category queries

-- name: CreateCategory :exec
INSERT INTO object_categories (id, tenant_id, slug, name, description)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCategory :one
SELECT id, tenant_id, slug, name, description, created_at, updated_at
FROM object_categories
WHERE tenant_id = $1 AND slug = $2;

-- name: ListCategories :many
SELECT id, tenant_id, slug, name, description, created_at, updated_at
FROM object_categories
WHERE tenant_id = $1
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor'))
ORDER BY created_at DESC
LIMIT $2;

-- name: DeleteCategory :execrows
DELETE FROM object_categories
WHERE tenant_id = $1 AND slug = $2;

-- name: CategoryExists :one
SELECT EXISTS(
  SELECT 1 FROM object_categories
  WHERE tenant_id = $1 AND slug = $2
) AS exists;

-- name: CategoryObjectCount :one
SELECT COUNT(*)::bigint AS count
FROM objects
WHERE tenant_id = $1 AND category = $2
  AND status NOT IN ('hard_deleted');

-- name: ListTenants :many
SELECT DISTINCT tenant_id
FROM object_categories
ORDER BY tenant_id ASC;
