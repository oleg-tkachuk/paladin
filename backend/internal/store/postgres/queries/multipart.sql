-- Multipart upload queries.

-- name: CreateMultipartUpload :exec
INSERT INTO multipart_uploads (
    upload_id, object_id, storage_upload_id, part_size_bytes, total_parts
) VALUES ($1, $2, $3, $4, $5);

-- name: GetMultipartUpload :one
SELECT sqlc.embed(multipart_uploads)
FROM multipart_uploads
WHERE upload_id = $1;

-- name: DeleteMultipartUpload :exec
DELETE FROM multipart_uploads
WHERE upload_id = $1;

-- name: ListStaleMultipartUploads :many
-- Sessions whose client never Completed/Aborted, past the cooling-off
-- window. Joins objects + object_keys to materialise everything
-- AbortMultipart needs (bucket, tenant, storage upload id, key) so the
-- reaper aborts the S3-side session (which otherwise accrues part-storage
-- charges forever) in one round-trip per row. Bounded by batch_size.
SELECT m.upload_id, m.storage_upload_id,
       o.object_id, o.tenant_id, o.object_key, o.key,
       k.bucket_name
FROM multipart_uploads m
JOIN objects o      ON o.object_id = m.object_id
JOIN object_keys k  ON k.tenant_id = o.tenant_id AND k.object_key = o.object_key
WHERE m.created_at < $1
ORDER BY m.created_at
LIMIT sqlc.arg('batch_size');

-- name: RecordMultipartPart :exec
INSERT INTO multipart_parts (upload_id, part_number, size_bytes, etag, checksum)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (upload_id, part_number) DO UPDATE
SET size_bytes = EXCLUDED.size_bytes,
    etag       = EXCLUDED.etag,
    checksum   = EXCLUDED.checksum,
    uploaded_at = now();

-- name: ListMultipartParts :many
SELECT upload_id, part_number, size_bytes, etag, checksum, uploaded_at
FROM multipart_parts
WHERE upload_id = $1
ORDER BY part_number;
