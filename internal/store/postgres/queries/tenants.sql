-- Tenant queries

-- name: UpsertTenant :one
INSERT INTO tenants (id, tenant_id, display_name, labels, tags)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id) DO UPDATE
    SET display_name = EXCLUDED.display_name,
        labels       = EXCLUDED.labels,
        tags         = EXCLUDED.tags,
        updated_at   = now()
RETURNING id, tenant_id, display_name, labels, tags, created_at, updated_at;

-- name: GetTenant :one
SELECT id, tenant_id, display_name, labels, tags, created_at, updated_at
FROM tenants
WHERE tenant_id = $1;

-- name: DeleteTenant :execrows
DELETE FROM tenants
WHERE tenant_id = $1;

-- name: TenantHasActiveObjects :one
SELECT EXISTS(
    SELECT 1
    FROM objects
    WHERE tenant_id = $1
      AND status NOT IN ('hard_deleted')
) AS has_active_objects;

-- Labels patch: merge existing labels with the patch, stripping null values.
-- This means:
--   - Provided keys overwrite existing keys.
--   - Provided keys with JSON null values are removed.
--   - Keys absent from the patch are preserved.
-- name: UpdateTenantMetadata :one
UPDATE tenants
SET labels = jsonb_strip_nulls(labels || $2),
    tags = $3,
    display_name = COALESCE($4, display_name),
    updated_at = now()
WHERE tenant_id = $1
RETURNING id, tenant_id, display_name, labels, tags, created_at, updated_at;

-- name: ListTenantsPaginated :many
SELECT id, tenant_id, display_name, labels, tags, created_at, updated_at,
       COUNT(*) OVER () AS total_count
FROM tenants
WHERE
    -- cursor: only return rows created before this timestamp (DESC ordering)
    (@cursor::timestamptz IS NULL OR created_at < @cursor::timestamptz)
    -- label containment filter: tenant.labels must contain all provided key-value pairs
    AND (@label_selector::jsonb IS NULL OR labels @> @label_selector::jsonb)
    -- tag overlap filter: tenant.tags must have at least one tag from the list
    AND (@tag_selector::text[] IS NULL OR tags && @tag_selector::text[])
    -- search filter
    AND (
        @search::text IS NULL OR
        tenant_id ILIKE '%' || @search || '%' OR
        display_name ILIKE '%' || @search || '%'
    )
ORDER BY
    -- Sorting logic
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'asc' THEN display_name END ASC,
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'desc' THEN display_name END DESC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND @sort_order::text = 'asc' THEN created_at END ASC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND (@sort_order::text = 'desc' OR @sort_order::text IS NULL) THEN created_at END DESC
LIMIT @limit_val;
