-- Multipart queries

-- name: CreateMultipart :exec
INSERT INTO multipart_uploads (
    id, tenant_id, object_id, upload_id, bucket, object_key, 
    content_type, part_size_bytes, status, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
);

-- name: GetMultipartByUploadID :one
SELECT 
    id, tenant_id, object_id, upload_id, bucket, object_key, 
    content_type, part_size_bytes, status, created_at, updated_at, expires_at
FROM multipart_uploads
WHERE tenant_id = $1 AND upload_id = $2;

-- name: UpsertMultipartPart :exec
INSERT INTO multipart_parts (multipart_id, part_number, etag, size_bytes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (multipart_id, part_number)
DO UPDATE SET 
    etag = EXCLUDED.etag, 
    size_bytes = EXCLUDED.size_bytes;

-- name: ListMultipartParts :many
SELECT multipart_id, part_number, etag, size_bytes, created_at
FROM multipart_parts
WHERE multipart_id = $1
ORDER BY part_number ASC;

-- name: MarkMultipartCompleted :exec
UPDATE multipart_uploads 
SET status = 'completed', updated_at = now()
WHERE tenant_id = $1 AND upload_id = $2;

-- name: MarkMultipartAborted :exec
UPDATE multipart_uploads 
SET status = 'aborted', updated_at = now()
WHERE tenant_id = $1 AND upload_id = $2;

-- name: UpdateMultipartStatus :execrows
UPDATE multipart_uploads 
SET status = 'completed', updated_at = now()
WHERE tenant_id = $1 AND upload_id = $2 AND status = 'initiated';

-- name: UpdateObjectStatusToActive :execrows
UPDATE objects 
SET status = 'active', updated_at = now()
WHERE id = $1 AND tenant_id = $2;

-- name: ListExpiredMultiparts :many
SELECT 
    id, tenant_id, object_id, upload_id, bucket, object_key, 
    content_type, part_size_bytes, status, created_at, updated_at, expires_at
FROM multipart_uploads
WHERE status = 'initiated' AND expires_at < NOW()
LIMIT $1;
