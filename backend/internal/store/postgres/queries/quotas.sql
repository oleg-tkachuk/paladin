-- name: UpsertTenantQuota :exec
INSERT INTO quotas (id, tenant_id, max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id) WHERE bucket_id IS NULL DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    updated_at          = now();

-- name: UpsertBucketQuota :exec
INSERT INTO quotas (id, bucket_id, max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day)
VALUES ($1, (SELECT b.id FROM buckets b
                  JOIN storage_backends sb ON sb.id = b.backend_id
                 WHERE sb.name = $2 AND b.name = $3), $4, $5, $6, $7)
ON CONFLICT (bucket_id) WHERE tenant_id IS NULL DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    updated_at          = now();

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
UPDATE quotas
SET usage_total_bytes   = usage_total_bytes + $2,
    usage_object_count  = usage_object_count + $3,
    usage_bytes_today   = usage_bytes_today + $2,
    usage_objects_today = usage_objects_today + $3
WHERE id = $1;

-- name: ResetQuotaDaily :exec
UPDATE quotas
SET usage_bytes_today = 0,
    usage_objects_today = 0,
    last_reset_at = $2
WHERE id = $1;
