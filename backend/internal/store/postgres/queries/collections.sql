-- Collection queries.

-- name: CreateCollection :exec
INSERT INTO collections (
    tenant_id, collection, display_name, backend_id, bucket_name,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetCollection :one
SELECT sqlc.embed(collections)
FROM collections
WHERE tenant_id = $1 AND collection = $2;

-- name: ResolveCollectionPrefix :one
-- Longest registered collection that is a prefix of $2 (the recombined
-- "<collection>/<key>" tail of an ingest event) for the tenant. Multi-segment
-- collections (migration 030) make the naive "the OK is the first path
-- segment" split ambiguous — e.g. tail `invoices/2026/q1/report.pdf` could be
-- OK `invoices` + key `2026/q1/report.pdf` OR OK `invoices/2026/q1` + key
-- `report.pdf`. Longest-prefix gives deterministic precedence (the more
-- specific OK wins). collection is constrained to `[a-z0-9-]` path segments
-- (migration 030 / 001) — no LIKE metacharacters — so `|| '/%'` is safe.
SELECT collection
FROM collections
WHERE tenant_id = $1
  AND ($2 = collection OR $2 LIKE collection || '/%')
ORDER BY length(collection) DESC
LIMIT 1;

-- name: ListCollectionNamesForTenant :many
-- Every registered collection name for the tenant. Backs the in-process
-- longest-prefix cache (eventingest.CachingLookup) so ResolveCollectionPrefix is
-- not a per-event query on the ingest hot path. collections is small per tenant
-- (bounded by the tenant's namespace layout), so the unbounded read is cheap.
SELECT collection
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
WHERE tenant_id = $1 AND collection = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListCollections :many
SELECT sqlc.embed(collections)
FROM collections
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL OR collection > sqlc.narg('after_id')::text)
ORDER BY collection
LIMIT sqlc.arg('page_size');

-- name: BindCollectionToBucket :execrows
-- Atomically rebinds an collection to a different (backend_id, bucket_name).
-- The DB trigger enforce_collection_bucket_tenancy validates the tenancy
-- constraint (single-tenant buckets reject mismatched tenants).
UPDATE collections
SET backend_id  = $3,
    bucket_name = $4
WHERE tenant_id = $1 AND collection = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteCollection :execrows
DELETE FROM collections
WHERE tenant_id = $1 AND collection = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: GetEffectivePolicy :one
-- Returns tenant-inherited policy concatenated with the collection-specific policy.
-- Order is: tenant policies first, then collection — Cedar treats them as a single
-- policy set; ordering only affects diagnostic output.
SELECT t.inherited_cedar_policy AS tenant_policy,
       t.inherited_policy_hash  AS tenant_hash,
       b.cedar_policy           AS bucket_policy,
       b.cedar_policy_hash      AS bucket_hash
FROM tenants t
LEFT JOIN collections b
  ON b.tenant_id = t.tenant_id
 AND b.collection = sqlc.narg('collection')::text
WHERE t.tenant_id = $1;
