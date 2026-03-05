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

-- name: UpdateTenantMetadata :one
-- Labels patch: merge existing labels with the patch, stripping null values.
-- This means:
--   - Provided keys overwrite existing keys.
--   - Provided keys with JSON null values are removed.
--   - Keys absent from the patch are preserved.
-- Tags: full replacement.
UPDATE tenants
SET labels     = jsonb_strip_nulls(labels || $2::jsonb),
    tags       = $3::text[],
    updated_at = now()
WHERE tenant_id = $1
RETURNING id, tenant_id, display_name, labels, tags, created_at, updated_at;

-- name: ListTenantsPaginated :many
SELECT id, tenant_id, display_name, labels, tags, created_at, updated_at,
       COUNT(*) OVER () AS total_count
FROM tenants
WHERE
    -- cursor: only return rows created before this timestamp (DESC ordering)
    ($1::timestamptz IS NULL OR created_at < $1::timestamptz)
    -- label containment filter: tenant.labels must contain all provided key-value pairs
    AND ($2::jsonb IS NULL OR labels @> $2::jsonb)
    -- tag overlap filter: tenant.tags must have at least one tag from the list
    AND ($3::text[] IS NULL OR tags && $3::text[])
ORDER BY created_at DESC
LIMIT $4;

