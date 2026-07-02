-- Tenant queries.

-- name: CreateTenant :exec
INSERT INTO tenants (tenant_id, slug, display_name, labels, inherited_cedar_policy, storage_layout)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetTenant :one
-- LEFT JOIN tenant_default_bindings: 0/1 row per tenant (tenant_id is its PK),
-- so the embed stays single-row. backend_id/bucket_name are NULL when unbound.
SELECT sqlc.embed(tenants), tdb.backend_id, tdb.bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.tenant_id
WHERE tenants.tenant_id = $1;

-- name: GetTenantBySlug :one
SELECT sqlc.embed(tenants), tdb.backend_id, tdb.bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.tenant_id
WHERE tenants.slug = $1;

-- name: UpdateTenant :execrows
UPDATE tenants
SET display_name           = COALESCE(sqlc.narg('display_name'), display_name),
    labels                 = COALESCE(sqlc.narg('labels'),       labels),
    inherited_cedar_policy = COALESCE(sqlc.narg('policy'),       inherited_cedar_policy),
    inherited_policy_hash  = CASE WHEN sqlc.narg('policy') IS NULL
                                  THEN inherited_policy_hash
                                  ELSE sqlc.narg('policy_hash') END
WHERE tenant_id = $1
  AND resource_version = sqlc.arg('expected_version');

-- name: ListTenants :many
-- include_trashed = false → active rows only; true → both;
-- only_trashed = true → trashed only (overrides include_trashed).
-- The boolean gating is inline-CASE so sqlc emits a single prepared
-- statement; planner uses the partial idx_tenants_active index on
-- the common path.
SELECT sqlc.embed(tenants), tdb.backend_id, tdb.bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.tenant_id
WHERE (sqlc.narg('after_id')::uuid IS NULL OR tenants.tenant_id > sqlc.narg('after_id')::uuid)
  AND (
    CASE
      WHEN sqlc.arg('only_trashed')::bool      THEN tenants.deleted_at IS NOT NULL
      WHEN sqlc.arg('include_trashed')::bool   THEN TRUE
      ELSE                                          tenants.deleted_at IS NULL
    END
  )
ORDER BY tenants.tenant_id
LIMIT sqlc.arg('page_size');

-- name: SoftDeleteTenant :execrows
-- Sets deleted_at on an active row. expected_version=0 means
-- "no OCC guard" (legacy / scripted path); a non-zero value enforces
-- the match. Updates resource_version + updated_at so audit reflects
-- the soft-delete time independently of any subsequent restore.
UPDATE tenants
   SET deleted_at = now(),
       updated_at = now(),
       resource_version = resource_version + 1
 WHERE tenant_id = $1
   AND deleted_at IS NULL
   AND (sqlc.arg('expected_version')::bigint = 0
        OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: HardDeleteTenant :execrows
-- Unconditional physical delete. Used by Delete(force=true) and Purge.
-- expected_version=0 → no OCC guard; non-zero → strict match.
DELETE FROM tenants
WHERE tenant_id = $1
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: RestoreTenant :execrows
-- Clears deleted_at on a trashed row. Bumps resource_version +
-- updated_at.
UPDATE tenants
   SET deleted_at = NULL,
       updated_at = now(),
       resource_version = resource_version + 1
 WHERE tenant_id = $1
   AND deleted_at IS NOT NULL;
