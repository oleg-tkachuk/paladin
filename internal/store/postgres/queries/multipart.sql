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
