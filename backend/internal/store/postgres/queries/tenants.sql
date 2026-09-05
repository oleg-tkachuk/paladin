-- Tenant queries.

-- name: CreateTenant :exec
INSERT INTO tenants (id, slug, display_name, labels, inherited_cedar_policy, storage_layout)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetTenant :one
-- LEFT JOIN tenant_default_bindings: 0/1 row per tenant (tenant_id is its unique key),
-- so the embed stays single-row. backend_id/bucket_name are NULL when unbound.
SELECT sqlc.embed(tenants),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.id
LEFT JOIN buckets b           ON b.id = tdb.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE tenants.id = $1;

-- name: GetTenantBySlug :one
SELECT sqlc.embed(tenants),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.id
LEFT JOIN buckets b           ON b.id = tdb.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE tenants.slug = $1;

-- name: UpdateTenant :execrows
UPDATE tenants
SET display_name           = COALESCE(sqlc.narg('display_name'), display_name),
    labels                 = COALESCE(sqlc.narg('labels'),       labels),
    inherited_cedar_policy = COALESCE(sqlc.narg('policy'),       inherited_cedar_policy),
    inherited_policy_hash  = CASE WHEN sqlc.narg('policy') IS NULL
                                  THEN inherited_policy_hash
                                  ELSE sqlc.narg('policy_hash') END
WHERE id = $1
  AND resource_version = sqlc.arg('expected_version');

-- name: ListTenants :many
-- include_trashed = false → active rows only; true → both;
-- only_trashed = true → trashed only (overrides include_trashed).
-- The boolean gating is inline-CASE so sqlc emits a single prepared
-- statement; planner uses the partial idx_tenants_active index on
-- the common path.
SELECT sqlc.embed(tenants),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.id
LEFT JOIN buckets b           ON b.id = tdb.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE (sqlc.narg('after_id')::uuid IS NULL OR tenants.id > sqlc.narg('after_id')::uuid)
  AND (
    CASE
      WHEN sqlc.arg('only_trashed')::bool      THEN tenants.deleted_at IS NOT NULL
      WHEN sqlc.arg('include_trashed')::bool   THEN TRUE
      ELSE                                          tenants.deleted_at IS NULL
    END
  )
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  AND (sqlc.narg('slug_eq')::text IS NULL OR tenants.slug = sqlc.narg('slug_eq')::text)
  AND (sqlc.narg('slug_like')::text IS NULL OR tenants.slug LIKE sqlc.narg('slug_like')::text)
  AND (sqlc.narg('display_name_eq')::text IS NULL OR tenants.display_name = sqlc.narg('display_name_eq')::text)
  AND (sqlc.narg('display_name_like')::text IS NULL OR tenants.display_name LIKE sqlc.narg('display_name_like')::text)
  -- The derived `search` field, spelled to match cel.SearchText EXACTLY.
  -- ASCII-only folding via COLLATE "C": Go's strings.ToLower and Postgres
  -- lower() are two Unicode implementations and may disagree, and a
  -- disagreement here drops a row the authoritative CEL pass accepts. See
  -- internal/filter/cel/searchtext.go.
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(tenants.id::text COLLATE "C") || chr(10)
          || lower(tenants.slug COLLATE "C") || chr(10)
          || lower(coalesce(tenants.display_name, '') COLLATE "C")
          LIKE sqlc.narg('search_like')::text)
  -- Compared as text on purpose: the literal comes from a caller's filter, and
  -- casting an arbitrary string to the enum makes Postgres reject the whole
  -- query ("invalid input value for enum") instead of returning no rows.
  AND (sqlc.narg('storage_layout')::text IS NULL
       OR tenants.storage_layout::text = sqlc.narg('storage_layout')::text)
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR tenants.created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR tenants.created_at <= sqlc.narg('created_at_lte')::timestamptz)
ORDER BY tenants.id
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
 WHERE id = $1
   AND deleted_at IS NULL
   AND (sqlc.arg('expected_version')::bigint = 0
        OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: HardDeleteTenant :execrows
-- Unconditional physical delete. Used by Delete(force=true) and Purge.
-- expected_version=0 → no OCC guard; non-zero → strict match.
DELETE FROM tenants
WHERE id = $1
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: RestoreTenant :execrows
-- Clears deleted_at on a trashed row. Bumps resource_version +
-- updated_at.
UPDATE tenants
   SET deleted_at = NULL,
       updated_at = now(),
       resource_version = resource_version + 1
 WHERE id = $1
   AND deleted_at IS NOT NULL;
