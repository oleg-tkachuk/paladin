-- Category queries

-- name: CreateCategory :exec
INSERT INTO object_categories (id, tenant_id, slug, name, description)
VALUES ($1, $2, $3, $4, $5);

-- name: UpdateCategory :exec
UPDATE object_categories
SET name = $3,
    description = $4,
    updated_at = now()
WHERE tenant_id = $1 AND slug = $2;

-- name: GetCategory :one
SELECT sqlc.embed(object_categories)
FROM object_categories
WHERE tenant_id = $1 AND slug = $2;

-- name: ListCategories :many
SELECT id, tenant_id, slug, name, description, created_at, updated_at,
       COUNT(*) OVER() AS total_count
FROM object_categories
WHERE tenant_id = @tenant_id
  AND (@cursor::timestamptz IS NULL OR created_at < @cursor::timestamptz)
  AND (
    @search::text IS NULL OR 
    slug ILIKE '%' || @search || '%' OR 
    name ILIKE '%' || @search || '%'
  )
ORDER BY
    CASE WHEN @sort_by::text = 'slug' AND @sort_order::text = 'asc' THEN slug END ASC,
    CASE WHEN @sort_by::text = 'slug' AND @sort_order::text = 'desc' THEN slug END DESC,
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'asc' THEN name END ASC,
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'desc' THEN name END DESC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND @sort_order::text = 'asc' THEN created_at END ASC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND (@sort_order::text = 'desc' OR @sort_order::text IS NULL) THEN created_at END DESC
LIMIT @limit_val;

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
  AND status NOT IN ('hard_deleted', 'soft_deleted');

-- name: ListTenants :many
SELECT tenant_id,
       first_created_at,
       COUNT(*) OVER() AS total_count
FROM (
    SELECT tenant_id, MIN(created_at)::timestamptz as first_created_at
    FROM object_categories
    GROUP BY tenant_id
) t
WHERE (sqlc.narg('cursor')::timestamptz IS NULL OR first_created_at < sqlc.narg('cursor'))
ORDER BY first_created_at DESC
LIMIT $1;

-- name: GetCategoryStats :one
SELECT 
    COUNT(*)::bigint AS total_count,
    COALESCE(SUM(size_bytes), 0)::bigint AS total_size
FROM objects
WHERE tenant_id = $1 AND category = $2
  AND status NOT IN ('hard_deleted', 'soft_deleted');
