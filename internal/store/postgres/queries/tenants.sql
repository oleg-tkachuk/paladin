-- Tenant queries.

-- name: CreateTenant :exec
INSERT INTO tenants (tenant_id, display_name, labels, inherited_cedar_policy)
VALUES ($1, $2, $3, $4);

-- name: GetTenant :one
SELECT sqlc.embed(tenants)
FROM tenants
WHERE tenant_id = $1;

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
SELECT sqlc.embed(tenants)
FROM tenants
WHERE (sqlc.narg('after_id')::uuid IS NULL OR tenant_id > sqlc.narg('after_id')::uuid)
ORDER BY tenant_id
LIMIT sqlc.arg('page_size');

-- name: DeleteTenant :execrows
DELETE FROM tenants
WHERE tenant_id = $1
  AND resource_version = sqlc.arg('expected_version');
