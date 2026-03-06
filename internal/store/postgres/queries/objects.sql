-- Objects queries

-- name: CreateObject :exec
INSERT INTO objects (
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, expires_at, labels, external_ref, category, subpath
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
);

-- name: GetObject :one
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND id = $2;

-- name: GetObjectByExternalRef :one
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND external_ref = $2;

-- name: MarkObjectComplete :execrows
UPDATE objects
SET status = 'complete',
    stored_etag = $3,
    stored_size_bytes = $4,
    completed_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND (status = 'pending' OR status = 'uploading');

-- name: MarkObjectSoftDeleted :execrows
UPDATE objects
SET status = 'soft_deleted',
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status != 'soft_deleted'
  AND status != 'hard_deleted';

-- name: RestoreObject :execrows
UPDATE objects
SET status = 'uploaded',
    deleted_at = NULL,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'soft_deleted';

-- name: MarkObjectHardDeleted :execrows
UPDATE objects
SET status = 'hard_deleted',
    deleted_at = COALESCE(deleted_at, now()),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status != 'hard_deleted';

-- name: ListObjects :many
SELECT sqlc.embed(objects), COUNT(*) OVER() AS total_count
FROM objects
WHERE tenant_id = @tenant_id
  AND (@status::text IS NULL OR status = @status)
  AND (@external_ref::text IS NULL OR external_ref = @external_ref)
  AND (@created_after::timestamptz IS NULL OR created_at >= @created_after::timestamptz)
  AND (@created_before::timestamptz IS NULL OR created_at < @created_before::timestamptz)
  AND (@cursor::timestamptz IS NULL OR created_at < @cursor::timestamptz)
  AND (
    @category::text IS NULL OR 
    (@recursive::bool AND (category = @category OR category LIKE @category || '/%')) OR
    category = @category
  )
  AND (@key_prefix::text IS NULL OR object_key LIKE @key_prefix || '%')
ORDER BY
    -- Sorting logic
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'asc' THEN object_key END ASC,
    CASE WHEN @sort_by::text = 'name' AND @sort_order::text = 'desc' THEN object_key END DESC,
    CASE WHEN @sort_by::text = 'category' AND @sort_order::text = 'asc' THEN category END ASC,
    CASE WHEN @sort_by::text = 'category' AND @sort_order::text = 'desc' THEN category END DESC,
    CASE WHEN @sort_by::text = 'size' AND @sort_order::text = 'asc' THEN size_bytes END ASC,
    CASE WHEN @sort_by::text = 'size' AND @sort_order::text = 'desc' THEN size_bytes END DESC,
    CASE WHEN @sort_by::text = 'status' AND @sort_order::text = 'asc' THEN status END ASC,
    CASE WHEN @sort_by::text = 'status' AND @sort_order::text = 'desc' THEN status END DESC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND @sort_order::text = 'asc' THEN created_at END ASC,
    CASE WHEN (@sort_by::text = 'created' OR @sort_by::text IS NULL) AND (@sort_order::text = 'desc' OR @sort_order::text IS NULL) THEN created_at END DESC
LIMIT @limit_val;

-- name: UpdateObjectStatus :execrows
UPDATE objects
SET status = $3, updated_at = NOW()
WHERE tenant_id = $1 AND id = $2;

-- name: PatchObjectLabels :one
UPDATE objects
SET labels = labels || $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING sqlc.embed(objects);

-- name: PatchObjectExternalRef :one
UPDATE objects
SET external_ref = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING sqlc.embed(objects);

-- name: PatchObjectLabelsAndExternalRef :one
UPDATE objects
SET labels = labels || $3,
    external_ref = $4,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING sqlc.embed(objects);

-- name: ListExpiredPendingObjects :many
SELECT sqlc.embed(objects)
FROM objects
WHERE status = 'pending' AND expires_at < $1
LIMIT $2;

-- name: DeleteObject :execrows
DELETE FROM objects
WHERE tenant_id = $1 AND id = $2;

-- name: BulkMarkObjectSoftDeleted :execrows
UPDATE objects
SET status = 'soft_deleted',
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = ANY($2::uuid[])
  AND status != 'soft_deleted'
  AND status != 'hard_deleted';

-- name: BulkRestoreObject :execrows
UPDATE objects
SET status = 'uploaded',
    deleted_at = NULL,
    updated_at = now()
WHERE tenant_id = $1
  AND id = ANY($2::uuid[])
  AND status = 'soft_deleted';

-- name: BulkDeleteObject :execrows
DELETE FROM objects
WHERE tenant_id = $1 AND id = ANY($2::uuid[]);

-- name: GetObjectStats :one
SELECT 
    COUNT(*)::bigint as total_count,
    COALESCE(SUM(size_bytes), 0)::bigint as total_size,
    COUNT(*) FILTER (WHERE status = 'pending')::bigint as pending_count,
    COUNT(*) FILTER (WHERE status = 'uploading')::bigint as uploading_count,
    COUNT(*) FILTER (WHERE status = 'uploaded')::bigint as uploaded_count,
    COUNT(*) FILTER (WHERE status = 'complete')::bigint as complete_count,
    COUNT(*) FILTER (WHERE status = 'soft_deleted')::bigint as soft_deleted_count
FROM objects
WHERE tenant_id = $1;
