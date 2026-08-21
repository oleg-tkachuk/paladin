-- Collection queries.

-- name: CreateCollection :exec
INSERT INTO collections (
    tenant_id, name, display_name, bucket_id,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, (SELECT b.id FROM buckets b
            JOIN storage_backends sb ON sb.id = b.backend_id
           WHERE sb.name = $4 AND b.name = $5), $6, $7);

-- name: GetCollection :one
-- Returns the backend and bucket by NAME alongside the row: callers build
-- resource names from this, and a resource name made of uuids would not
-- resolve back to anything a client can use.
SELECT sqlc.embed(collections),
       sb.name AS backend_name,
       b.name  AS bucket_name
FROM collections
JOIN buckets b           ON b.id = collections.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE collections.tenant_id = $1 AND collections.name = $2;

-- name: ResolveCollectionPrefix :one
-- Longest registered collection name that is a prefix of the candidate (the
-- recombined "<collection>/<path>" tail of an ingest event) for the tenant. Multi-segment
-- collections (the schema baseline (001_initial_schema.sql)) make the naive "the OK is the first path
-- segment" split ambiguous — e.g. tail `invoices/2026/q1/report.pdf` could be
-- collection `invoices` + path `2026/q1/report.pdf`, OR collection
-- `invoices/2026/q1` + path `report.pdf`. Longest-prefix is deterministic (the
-- more specific one wins). name is constrained to `[a-z0-9-]` path segments
-- (the schema baseline (001_initial_schema.sql)) — no LIKE metacharacters — so `|| '/%'` is safe.
SELECT name
FROM collections
WHERE tenant_id = $1
  AND (sqlc.arg('candidate')::text = name
       OR sqlc.arg('candidate')::text LIKE name || '/%')
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
SELECT sqlc.embed(collections),
       sb.name AS backend_name,
       b.name  AS bucket_name
FROM collections
JOIN buckets b           ON b.id = collections.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE collections.tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL
       OR collections.name > sqlc.narg('after_id')::text)
ORDER BY collections.name
LIMIT sqlc.arg('page_size');

-- name: BindCollectionToBucket :execrows
-- Atomically rebinds a collection to a different bucket. Tenancy is enforced
-- declaratively now: objects carry a composite FK to (tenant_id, id), so a
-- name cannot be moved under a bucket that would orphan them.
UPDATE collections
SET bucket_id = (SELECT b.id FROM buckets b
            JOIN storage_backends sb ON sb.id = b.backend_id
           WHERE sb.name = $3 AND b.name = $4)
WHERE collections.tenant_id = $1 AND collections.name = $2
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
