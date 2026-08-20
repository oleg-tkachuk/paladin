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
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (bucket_id) WHERE tenant_id IS NULL DO UPDATE SET
    max_total_bytes     = EXCLUDED.max_total_bytes,
    max_object_count    = EXCLUDED.max_object_count,
    max_bytes_per_day   = EXCLUDED.max_bytes_per_day,
    max_objects_per_day = EXCLUDED.max_objects_per_day,
    updated_at          = now();

-- name: GetTenantQuota :one
SELECT id, tenant_id, bucket_id,
       max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
       usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
       last_reset_at, resource_version, updated_at
FROM quotas
WHERE tenant_id = $1;

-- name: GetBucketQuota :one
SELECT id, tenant_id, bucket_id,
       max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
       usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
       last_reset_at, resource_version, updated_at
FROM quotas
WHERE bucket_id = $1;

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
