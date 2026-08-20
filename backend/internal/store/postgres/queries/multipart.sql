-- Multipart upload queries.

-- name: CreateMultipartUpload :exec
-- backend_id / bucket_name anchor the upload to the physical location resolved
-- at initiate time, so the rest of the lifecycle targets it regardless of a
-- later collection rebind (see migration 053).
INSERT INTO multipart_uploads (
    upload_id, object_id, storage_upload_id, part_size_bytes, total_parts,
    backend_id, bucket_name
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetMultipartUpload :one
SELECT sqlc.embed(multipart_uploads)
FROM multipart_uploads
WHERE upload_id = $1;

-- name: DeleteMultipartUpload :exec
DELETE FROM multipart_uploads
WHERE upload_id = $1;

-- name: ListStaleMultipartUploads :many
-- Sessions whose client never Completed/Aborted, past the cooling-off
-- window. Joins objects + collections to materialise everything
-- AbortMultipart needs (backend, bucket, tenant, storage upload id, key) so
-- the reaper aborts the S3-side session (which otherwise accrues part-storage
-- charges forever) on the backend the parts actually live on, in one
-- round-trip per row. Prefer the session-anchored location (migration 053);
-- fall back to the collection's current binding for legacy rows initiated
-- before the anchor columns existed. Bounded by batch_size.
SELECT m.upload_id, m.storage_upload_id,
       o.object_id, o.tenant_id, o.collection, o.key,
       COALESCE(NULLIF(m.backend_id, ''), k.backend_id)   AS backend_id,
       COALESCE(NULLIF(m.bucket_name, ''), k.bucket_name) AS bucket_name
FROM multipart_uploads m
JOIN objects o      ON o.object_id = m.object_id
JOIN collections k  ON k.tenant_id = o.tenant_id AND k.collection = o.collection
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
