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
ON CONFLICT (tenant_id) WHERE bucket_id IS NULL DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    resource_version    = quotas.resource_version + 1,
    updated_at          = now()
WHERE quotas.resource_version = sqlc.arg('expected_version')::bigint
RETURNING resource_version;

-- name: UpsertBucketQuota :one
-- Same OCC contract as UpsertTenantQuota.
INSERT INTO quotas (id, bucket_id, max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day)
VALUES ($1, (SELECT b.id FROM buckets b
                  JOIN storage_backends sb ON sb.id = b.backend_id
                 WHERE sb.name = $2 AND b.name = $3), $4, $5, $6, $7)
ON CONFLICT (bucket_id) WHERE tenant_id IS NULL DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    resource_version    = quotas.resource_version + 1,
    updated_at          = now()
WHERE quotas.resource_version = sqlc.arg('expected_version')::bigint
RETURNING resource_version;

-- name: GetTenantQuota :one
-- LEFT JOIN: a tenant-scoped quota has no bucket, and must still come back.
SELECT sqlc.embed(quotas),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM quotas
LEFT JOIN buckets b           ON b.id = quotas.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE tenant_id = $1;

-- name: GetBucketQuota :one
-- LEFT JOIN: a tenant-scoped quota has no bucket, and must still come back.
SELECT sqlc.embed(quotas),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM quotas
LEFT JOIN buckets b           ON b.id = quotas.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE bucket_id = (SELECT b.id FROM buckets b
                  JOIN storage_backends sb ON sb.id = b.backend_id
                 WHERE sb.name = $1 AND b.name = $2);

-- name: IncrementQuotaUsage :exec
-- Atomic add. tenant_id-scoped quota when bucket fields are NULL.
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

-- name: GetQuotaByID :one
-- Either scope. Under RLS the caller sees its own tenant's rows; a platform
-- admin's cross-tenant read sees every row.
SELECT sqlc.embed(quotas),
       COALESCE(sb.name, '') AS backend_name,
       COALESCE(b.name, '')  AS bucket_name
FROM quotas
LEFT JOIN buckets b           ON b.id = quotas.bucket_id
LEFT JOIN storage_backends sb ON sb.id = b.backend_id
WHERE quotas.id = $1;

-- name: ResetQuotaDaily :execrows
-- Rows, not exec: under RLS a row outside the session's tenant is filtered
-- out rather than refused, so zero rows is the only sign the reset missed.
UPDATE quotas
SET usage_bytes_today = 0,
    usage_objects_today = 0,
    last_reset_at = $2
WHERE id = $1;
