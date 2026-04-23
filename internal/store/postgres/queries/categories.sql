-- Category queries. Tenant-scoped; addressed by (tenant_id, slug).

-- name: CreateCategory :exec
INSERT INTO categories (tenant_id, slug, display_name, description, labels)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCategory :one
SELECT sqlc.embed(categories)
FROM categories
WHERE tenant_id = $1 AND slug = $2;

-- name: UpdateCategory :execrows
UPDATE categories
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    description  = COALESCE(sqlc.narg('description'),  description),
    labels       = COALESCE(sqlc.narg('labels'),       labels)
WHERE tenant_id = $1 AND slug = $2
  AND resource_version = sqlc.arg('expected_version');

-- name: ListCategories :many
SELECT sqlc.embed(categories)
FROM categories
WHERE tenant_id = $1
  AND (sqlc.narg('after_slug')::text IS NULL OR slug > sqlc.narg('after_slug')::text)
ORDER BY slug
LIMIT sqlc.arg('page_size');

-- name: DeleteCategory :execrows
DELETE FROM categories
WHERE tenant_id = $1 AND slug = $2
  AND resource_version = sqlc.arg('expected_version');
