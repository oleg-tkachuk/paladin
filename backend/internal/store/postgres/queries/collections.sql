-- Collection queries.

-- name: CreateCollection :exec
INSERT INTO collections (
    tenant_id, name, display_name, bucket_id,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetCollection :one
SELECT sqlc.embed(collections)
FROM collections
WHERE tenant_id = $1 AND name = $2;

-- name: ResolveCollectionPrefix :one
-- Longest registered name that is a prefix of $2 (the recombined
-- "<name>/<key>" tail of an ingest event) for the tenant. Multi-segment
-- collections (migration 030) make the naive "the OK is the first path
-- segment" split ambiguous — e.g. tail `invoices/2026/q1/report.pdf` could be
-- OK `invoices` + key `2026/q1/report.pdf` OR OK `invoices/2026/q1` + key
-- `report.pdf`. Longest-prefix gives deterministic precedence (the more
-- specific OK wins). name is constrained to `[a-z0-9-]` path segments
-- (migration 030 / 001) — no LIKE metacharacters — so `|| '/%'` is safe.
SELECT name
FROM collections
WHERE tenant_id = $1
  AND ($2 = name OR $2 LIKE name || '/%')
ORDER BY length(name) DESC
LIMIT 1;

-- name: ListCollectionNamesForTenant :many
-- Every registered name name for the tenant. Backs the in-process
-- longest-prefix cache (eventingest.CachingLookup) so ResolveCollectionPrefix is
-- not a per-event query on the ingest hot path. collections is small per tenant
-- (bounded by the tenant's namespace layout), so the unbounded read is cheap.
SELECT name
FROM collections
WHERE tenant_id = $1;

-- name: UpdateCollection :execrows
-- expected_version=0 disables the OCC guard (force update).
UPDATE collections
SET display_name    = COALESCE(sqlc.narg('display_name'),    display_name),
    cedar_policy    = COALESCE(sqlc.narg('policy'),          cedar_policy),
    cedar_policy_hash = CASE WHEN sqlc.narg('policy') IS NULL
                             THEN cedar_policy_hash
                             ELSE sqlc.narg('policy_hash') END,
    lifecycle_rules = COALESCE(sqlc.narg('lifecycle_rules'), lifecycle_rules)
WHERE tenant_id = $1 AND name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListCollections :many
SELECT sqlc.embed(collections)
FROM collections
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL OR name > sqlc.narg('after_id')::text)
ORDER BY name
LIMIT sqlc.arg('page_size');

-- name: BindCollectionToBucket :execrows
-- Atomically rebinds a name to a different bucket. Tenancy is enforced
-- declaratively now: objects carry a composite FK to (tenant_id, id), so a
-- name cannot be moved under a bucket that would orphan them.
UPDATE collections
SET bucket_id = $3
WHERE tenant_id = $1 AND name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteCollection :execrows
DELETE FROM collections
WHERE tenant_id = $1 AND name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: GetEffectivePolicy :one
-- Returns tenant-inherited policy concatenated with the collection's own.
-- Order is: tenant policies first, then name — Cedar treats them as a single
-- policy set; ordering only affects diagnostic output.
SELECT t.inherited_cedar_policy AS tenant_policy,
       t.inherited_policy_hash  AS tenant_hash,
       b.cedar_policy           AS bucket_policy,
       b.cedar_policy_hash      AS bucket_hash
FROM tenants t
LEFT JOIN collections b
  ON b.tenant_id = t.id
 AND b.name = sqlc.narg('collection')::text
WHERE t.id = $1;
