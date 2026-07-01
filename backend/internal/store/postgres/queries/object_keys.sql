-- ObjectKey queries.

-- name: CreateObjectKey :exec
INSERT INTO object_keys (
    tenant_id, object_key, display_name, backend_id, bucket_name,
    cedar_policy, lifecycle_rules
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetObjectKey :one
SELECT sqlc.embed(object_keys)
FROM object_keys
WHERE tenant_id = $1 AND object_key = $2;

-- name: ResolveObjectKeyPrefix :one
-- Longest registered object_key that is a prefix of $2 (the recombined
-- "<object_key>/<key>" tail of an ingest event) for the tenant. Multi-segment
-- object_keys (migration 030) make the naive "the OK is the first path
-- segment" split ambiguous — e.g. tail `invoices/2026/q1/report.pdf` could be
-- OK `invoices` + key `2026/q1/report.pdf` OR OK `invoices/2026/q1` + key
-- `report.pdf`. Longest-prefix gives deterministic precedence (the more
-- specific OK wins). object_key is constrained to `[a-z0-9-]` path segments
-- (migration 030 / 001) — no LIKE metacharacters — so `|| '/%'` is safe.
SELECT object_key
FROM object_keys
WHERE tenant_id = $1
  AND ($2 = object_key OR $2 LIKE object_key || '/%')
ORDER BY length(object_key) DESC
LIMIT 1;

-- name: ListObjectKeyNamesForTenant :many
-- Every registered object_key name for the tenant. Backs the in-process
-- longest-prefix cache (eventingest.CachingLookup) so ResolveObjectKeyPrefix is
-- not a per-event query on the ingest hot path. object_keys is small per tenant
-- (bounded by the tenant's namespace layout), so the unbounded read is cheap.
SELECT object_key
FROM object_keys
WHERE tenant_id = $1;

-- name: UpdateObjectKey :execrows
-- expected_version=0 disables the OCC guard (force update).
UPDATE object_keys
SET display_name    = COALESCE(sqlc.narg('display_name'),    display_name),
    cedar_policy    = COALESCE(sqlc.narg('policy'),          cedar_policy),
    cedar_policy_hash = CASE WHEN sqlc.narg('policy') IS NULL
                             THEN cedar_policy_hash
                             ELSE sqlc.narg('policy_hash') END,
    lifecycle_rules = COALESCE(sqlc.narg('lifecycle_rules'), lifecycle_rules)
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListObjectKeys :many
SELECT sqlc.embed(object_keys)
FROM object_keys
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::text IS NULL OR object_key > sqlc.narg('after_id')::text)
ORDER BY object_key
LIMIT sqlc.arg('page_size');

-- name: BindObjectKeyToBucket :execrows
-- Atomically rebinds an object_key to a different (backend_id, bucket_name).
-- The DB trigger enforce_object_key_bucket_tenancy validates the tenancy
-- constraint (single-tenant buckets reject mismatched tenants).
UPDATE object_keys
SET backend_id  = $3,
    bucket_name = $4
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteObjectKey :execrows
DELETE FROM object_keys
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: GetEffectivePolicy :one
-- Returns tenant-inherited policy concatenated with the object_key-specific policy.
-- Order is: tenant policies first, then object_key — Cedar treats them as a single
-- policy set; ordering only affects diagnostic output.
SELECT t.inherited_cedar_policy AS tenant_policy,
       t.inherited_policy_hash  AS tenant_hash,
       b.cedar_policy           AS bucket_policy,
       b.cedar_policy_hash      AS bucket_hash
FROM tenants t
LEFT JOIN object_keys b
  ON b.tenant_id = t.tenant_id
 AND b.object_key = sqlc.narg('object_key')::text
WHERE t.tenant_id = $1;
