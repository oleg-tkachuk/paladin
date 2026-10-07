-- Multipart upload queries.

-- name: CreateMultipartUpload :exec
-- bucket_id anchors the upload to the physical location resolved at initiate
-- time, so the rest of the lifecycle targets it regardless of a later
-- collection rebind. Resolved from the (backend, bucket) name pair here.
--
-- collection_name / path are denormalised copies of where the object lives.
-- They exist because the abort-debt trigger (010) has to write a
-- self-sufficient row: when the session is removed by a cascade from objects,
-- the object row is already gone and a join for the key returns nothing.
INSERT INTO multipart_uploads (
    id, tenant_id, object_id, storage_upload_id, part_size_bytes, total_parts,
    bucket_id, initiated_by_subject, initiated_by_kind, collection_name, path
) VALUES ($1, $2, $3, $4, $5, $6,
          (SELECT b.id FROM buckets b
             JOIN storage_backends sb ON sb.id = b.backend_id
            WHERE sb.name = $7 AND b.name = $8),
          $9, $10, sqlc.arg('collection_name'), sqlc.arg('path'));

-- name: RecordCompositeChecksum :execrows
-- Records a multipart object's composite checksum and its part size before
-- the object store assembles it. Written while the row is PENDING, so it is
-- in place whichever promotes the object first: this upload's completion, or
-- the storage event, which carries no checksum.
UPDATE objects
   SET checksum = sqlc.arg('checksum'),
       checksum_part_size_bytes = sqlc.arg('part_size_bytes')
 WHERE id = sqlc.arg('object_id') AND state = 'PENDING';

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


-- ─── Abort debt (010_multipart_abort_debt.sql) ──────────────────────────────
--
-- Rows the trigger wrote when a session was deleted without anyone telling S3
-- about it. Same contract as pending_purges: the debt outlives the rows whose
-- deletion created it, and is discharged by a drainer.

-- name: ClaimDueMultipartAborts :many
-- One drainer tick's worth. FOR UPDATE SKIP LOCKED so replicas take disjoint
-- rows; the join resolves the backend the parts actually live on, which is
-- why bucket_id carries a RESTRICT foreign key.
SELECT d.id, d.tenant_id, d.object_id, d.collection_name, d.path,
       d.storage_upload_id, d.attempts,
       sb.name AS backend_name,
       b.name  AS bucket_name
FROM pending_multipart_aborts d
JOIN buckets b           ON b.id = d.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE d.next_attempt_at <= now()
ORDER BY d.next_attempt_at
LIMIT sqlc.arg('batch_size')
FOR UPDATE OF d SKIP LOCKED;

-- name: DeleteMultipartAbortDebt :exec
-- The abort landed. S3 DELETE-style operations are idempotent, so a debt
-- discharged twice costs nothing — which is what makes "keep the row on any
-- doubt" the right failure policy.
DELETE FROM pending_multipart_aborts WHERE id = $1;

-- name: BackoffMultipartAbortDebt :exec
-- The abort failed. Record why and push the next attempt out; the debt is
-- never dropped, because nothing else in the system remembers those parts.
UPDATE pending_multipart_aborts
SET attempts        = attempts + 1,
    last_error      = sqlc.arg('last_error'),
    next_attempt_at = now() + (sqlc.arg('backoff_micros')::bigint || ' microseconds')::interval
WHERE id = $1;

-- name: CountPendingMultipartAborts :one
SELECT count(*) FROM pending_multipart_aborts;
