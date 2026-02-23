-- Objects queries

-- name: CreateObject :exec
INSERT INTO objects (
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, expires_at, labels, external_ref, category, subpath
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
);

-- name: GetObject :one
SELECT
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath
FROM objects
WHERE tenant_id = $1 AND id = $2;

-- name: GetObjectByExternalRef :one
SELECT
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath
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
SELECT
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath
FROM objects
WHERE tenant_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('external_ref')::text IS NULL OR external_ref = sqlc.narg('external_ref'))
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR created_at >= sqlc.narg('created_after'))
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR created_at < sqlc.narg('created_before'))
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor'))
  AND (sqlc.narg('category')::text IS NULL OR category = sqlc.narg('category'))
  AND (sqlc.narg('key_prefix')::text IS NULL OR object_key LIKE sqlc.narg('key_prefix') || '%')
ORDER BY created_at DESC
LIMIT $2;

-- name: UpdateObjectStatus :execrows
UPDATE objects
SET status = $3, updated_at = NOW()
WHERE tenant_id = $1 AND id = $2;

-- name: PatchObjectLabels :one
UPDATE objects
SET labels = labels || $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath;

-- name: PatchObjectExternalRef :one
UPDATE objects
SET external_ref = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath;

-- name: PatchObjectLabelsAndExternalRef :one
UPDATE objects
SET labels = labels || $3,
    external_ref = $4,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath;

-- name: ListExpiredPendingObjects :many
SELECT
    id, tenant_id, object_key, bucket, content_type, size_bytes,
    checksum_sha256, status, created_at, updated_at, expires_at,
    labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at,
    category, subpath
FROM objects
WHERE status = 'pending' AND expires_at < $1
LIMIT $2;

-- name: DeleteObject :execrows
DELETE FROM objects
WHERE tenant_id = $1 AND id = $2;

