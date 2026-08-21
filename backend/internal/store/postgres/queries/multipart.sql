-- Multipart upload queries.

-- name: CreateMultipartUpload :exec
-- bucket_id anchors the upload to the physical location resolved at initiate
-- time, so the rest of the lifecycle targets it regardless of a later
-- collection rebind. Resolved from the (backend, bucket) name pair here.
INSERT INTO multipart_uploads (
    id, tenant_id, object_id, storage_upload_id, part_size_bytes, total_parts,
    bucket_id, initiated_by_subject, initiated_by_kind
) VALUES ($1, $2, $3, $4, $5, $6,
          (SELECT b.id FROM buckets b
             JOIN storage_backends sb ON sb.id = b.backend_id
            WHERE sb.name = $7 AND b.name = $8),
          $9, $10);

-- name: DeleteMultipartUpload :exec
DELETE FROM multipart_uploads
WHERE id = $1;

-- name: ListStaleMultipartUploads :many
-- Sessions whose client never Completed/Aborted, past the cooling-off
-- window. Joins objects + collections to materialise everything
-- AbortMultipart needs (backend, bucket, tenant, storage upload id, key) so
-- the reaper aborts the S3-side session (which otherwise accrues part-storage
-- charges forever) on the backend the parts actually live on, in one
-- round-trip per row. Prefer the session-anchored location (the schema baseline (001_initial_schema.sql));
-- bucket_id is NOT NULL on multipart_uploads now, so the legacy COALESCE
-- fallback to the collection's binding is gone with the rows that needed it.
--
-- Names, not ids: the reaper aborts the upload against the storage backend,
-- which addresses buckets by name, and the collection name is a segment of the
-- object's storage path.
SELECT m.id, m.storage_upload_id,
       sb.name AS backend_name,
       b.name  AS bucket_name,
       k.name  AS collection_name,
       o.id AS object_id, o.tenant_id, o.path
FROM multipart_uploads m
JOIN objects o           ON o.id = m.object_id
JOIN buckets b           ON b.id = m.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
JOIN collections k       ON k.id = o.collection_id
WHERE m.created_at < $1
ORDER BY m.created_at
LIMIT sqlc.arg('batch_size');
