-- name: UpsertTenantQuota :one
-- OCC on update, no guard on insert.
--
-- The DO UPDATE's WHERE is the concurrency check: it fires only when the
-- stored resource_version equals what the caller read. A mismatch — including
-- a caller that passed 0 believing the row did not exist — updates nothing and
-- returns no row, which the adapter maps to Aborted. Without it this was a
-- blind last-writer-wins overwrite of another operator's limits.
INSERT INTO quotas (id, tenant_id, max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id) DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    resource_version    = quotas.resource_version + 1,
    updated_at          = now()
WHERE quotas.resource_version = sqlc.arg('expected_version')::bigint
RETURNING resource_version;

-- name: UpsertBucketQuota :one
-- Same OCC contract as UpsertTenantQuota. bucket_quotas has no RLS
-- (044_bucket_quotas.sql): the handler's role and Cedar checks gate the write.
INSERT INTO bucket_quotas (id, bucket_id, max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day)
VALUES ($1, (SELECT b.id FROM buckets b
                  JOIN storage_backends sb ON sb.id = b.backend_id
                 WHERE sb.name = $2 AND b.name = $3), $4, $5, $6, $7)
ON CONFLICT (bucket_id) DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    resource_version    = bucket_quotas.resource_version + 1,
    updated_at          = now()
WHERE bucket_quotas.resource_version = sqlc.arg('expected_version')::bigint
RETURNING resource_version;

-- name: GetTenantQuota :one
SELECT * FROM quotas WHERE tenant_id = $1;

-- name: GetBucketQuota :one
SELECT sqlc.embed(bucket_quotas),
       sb.name AS backend_name,
       b.name  AS bucket_name,
       b.owner_tenant_id
FROM bucket_quotas
JOIN buckets b           ON b.id = bucket_quotas.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = sqlc.arg('backend')::text AND b.name = sqlc.arg('bucket')::text;

-- name: GetQuotaByID :one
-- Tenant scope only. Under RLS the caller sees its own tenant's row; a
-- platform admin's cross-tenant read sees every row.
SELECT * FROM quotas WHERE id = $1;

-- name: GetBucketQuotaByID :one
SELECT sqlc.embed(bucket_quotas),
       sb.name AS backend_name,
       b.name  AS bucket_name,
       b.owner_tenant_id
FROM bucket_quotas
JOIN buckets b           ON b.id = bucket_quotas.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE bucket_quotas.id = $1;

-- name: IncrementQuotaUsage :exec
-- Atomic add.
-- The first charge of a UTC day restarts the per-day counters and stamps the
-- day. The daily roll skips rows with nothing to clear, so an idle row keeps
-- an older stamp, and the roll's next tick would otherwise zero what this day
-- had already admitted.
UPDATE quotas
SET usage_total_bytes   = usage_total_bytes + $2,
    usage_object_count  = usage_object_count + $3,
    usage_bytes_today   = CASE WHEN last_reset_at >= date_trunc('day', now(), 'UTC')
                               THEN usage_bytes_today + $2 ELSE $2 END,
    usage_objects_today = CASE WHEN last_reset_at >= date_trunc('day', now(), 'UTC')
                               THEN usage_objects_today + $3 ELSE $3 END,
    last_reset_at       = GREATEST(last_reset_at, date_trunc('day', now(), 'UTC'))
WHERE id = $1;

-- name: IncrementBucketQuotaUsageForObject :exec
-- Charges the quota of the bucket an object's collection is bound to — the
-- same object→collection→bucket hop the reconciler sums over — with the
-- day-roll of IncrementQuotaUsage. Updates nothing when the bucket has no
-- quota: quotas are opt-in. The lookup runs under the caller's RLS session,
-- which sees its own object and collection.
UPDATE bucket_quotas
SET usage_total_bytes   = usage_total_bytes + $2,
    usage_object_count  = usage_object_count + $3,
    usage_bytes_today   = CASE WHEN last_reset_at >= date_trunc('day', now(), 'UTC')
                               THEN usage_bytes_today + $2 ELSE $2 END,
    usage_objects_today = CASE WHEN last_reset_at >= date_trunc('day', now(), 'UTC')
                               THEN usage_objects_today + $3 ELSE $3 END,
    last_reset_at       = GREATEST(last_reset_at, date_trunc('day', now(), 'UTC'))
WHERE bucket_id = (SELECT c.bucket_id
                     FROM objects o
                     JOIN collections c ON c.id = o.collection_id
                    WHERE o.id = $1);

-- name: ResetQuotaDaily :execrows
-- Rows, not exec: under RLS a row outside the session's tenant is filtered
-- out rather than refused, so zero rows is the only sign the reset missed.
UPDATE quotas
SET usage_bytes_today = 0,
    usage_objects_today = 0,
    last_reset_at = $2
WHERE id = $1;

-- name: ResetBucketQuotaDaily :execrows
UPDATE bucket_quotas
SET usage_bytes_today = 0,
    usage_objects_today = 0,
    last_reset_at = $2
WHERE id = $1;
