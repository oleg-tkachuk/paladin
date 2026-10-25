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
-- A slug is unique among live tenants only (tenants_slug_live_key), so a
-- trashed tenant's slug can be taken by a live one. The slug names the live
-- tenant when there is one, else the most recently trashed: deterministic,
-- and what middleware.TenantFreeze resolves too (tenantstate.TenantIDBySlug).
SELECT sqlc.embed(tenants),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM tenants
LEFT JOIN tenant_default_bindings tdb ON tdb.tenant_id = tenants.id
LEFT JOIN buckets b           ON b.id = tdb.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE tenants.slug = $1
ORDER BY tenants.deleted_at IS NOT NULL, tenants.deleted_at DESC
LIMIT 1;

-- name: UpdateTenant :execrows
-- A tenant in the trash takes no changes (middleware.TenantFreeze); this is
-- the same rule where the row is written, for a caller the freeze could not
-- resolve.
UPDATE tenants
SET display_name           = COALESCE(sqlc.narg('display_name'), display_name),
    labels                 = COALESCE(sqlc.narg('labels'),       labels),
    inherited_cedar_policy = COALESCE(sqlc.narg('policy'),       inherited_cedar_policy),
    inherited_policy_hash  = CASE WHEN sqlc.narg('policy') IS NULL
                                  THEN inherited_policy_hash
                                  ELSE sqlc.narg('policy_hash') END
WHERE id = $1
  AND resource_version = sqlc.arg('expected_version')
  AND deleted_at IS NULL;

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
  AND (sqlc.narg('slug_in')::text[] IS NULL OR tenants.slug = ANY(sqlc.narg('slug_in')::text[]))
  AND (sqlc.narg('slug_like')::text IS NULL OR tenants.slug LIKE sqlc.narg('slug_like')::text)
  AND (sqlc.narg('display_name_in')::text[] IS NULL OR tenants.display_name = ANY(sqlc.narg('display_name_in')::text[]))
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
  AND (sqlc.narg('storage_layout_in')::text[] IS NULL
       OR tenants.storage_layout::text = ANY(sqlc.narg('storage_layout_in')::text[]))
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR tenants.created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR tenants.created_at <= sqlc.narg('created_at_lte')::timestamptz)
  -- The derived `purge_eligible` field: trashed, and its trash retention
  -- lapsed as of the request's own clock. The handler projects it from the
  -- same retention and the same instant (tenanth.tenantRow), so the two
  -- agree on every row, the boundary one included.
  AND (sqlc.narg('purge_eligible')::bool IS NULL
       OR (tenants.deleted_at IS NOT NULL
           AND tenants.deleted_at + make_interval(secs => sqlc.narg('trash_retention_seconds')::float8)
               <= sqlc.narg('as_of')::timestamptz) = sqlc.narg('purge_eligible')::bool)
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
-- Unconditional physical delete. Used by PurgeTenant.
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

-- name: GetTenantNames :many
-- The names of the tenants named, live or in the trash, for a listing that
-- joins its own rows to them by id (the tenant budget summary).
SELECT id, slug, display_name, deleted_at
FROM tenants
WHERE id = ANY(sqlc.arg('ids')::uuid[]);

-- name: CountTenantsPastTrashRetention :one
-- Trashed tenants whose trash retention has lapsed as of as_of: the ones the
-- console marks "Past retention" and an operator should decide on.
SELECT count(*) FROM tenants
WHERE deleted_at IS NOT NULL
  AND deleted_at + make_interval(secs => sqlc.arg('trash_retention_seconds')::float8)
      <= sqlc.arg('as_of')::timestamptz;

-- name: ListPurgeAPITokens :many
-- The tenant's API tokens a purge would remove, revoked ones included, in
-- issue order, up to the preview's limit. Under RLS: the caller acts on the
-- tenant (auth.WithActingTenant).
SELECT id, name
FROM api_tokens
WHERE tenant_id = sqlc.arg('tenant_id')
ORDER BY created_at, id
LIMIT sqlc.arg('max_rows');

-- name: CountPurgeAPITokens :one
SELECT count(*) FROM api_tokens WHERE tenant_id = sqlc.arg('tenant_id');

-- name: ListPurgeEventSubscriptions :many
-- The tenant's event subscriptions a purge would remove, in creation order,
-- up to the preview's limit.
SELECT id, sink_kind
FROM event_subscriptions
WHERE tenant_id = sqlc.arg('tenant_id')
ORDER BY created_at, id
LIMIT sqlc.arg('max_rows');

-- name: CountPurgeEventSubscriptions :one
SELECT count(*) FROM event_subscriptions WHERE tenant_id = sqlc.arg('tenant_id');

-- name: CountPurgeBlockers :one
-- The ON DELETE RESTRICT children of the tenant, which refuse its purge
-- while any is left (docs/deletion-semantics.md). Trashed collections count:
-- the foreign key does not tell them apart.
SELECT
    (SELECT count(*) FROM collections c WHERE c.tenant_id = sqlc.arg('tenant_id'))::bigint AS collections,
    (SELECT count(*) FROM buckets b WHERE b.owner_tenant_id = sqlc.arg('tenant_id'))::bigint AS buckets,
    (SELECT count(*) FROM users u WHERE u.tenant_id = sqlc.arg('tenant_id'))::bigint AS users;
