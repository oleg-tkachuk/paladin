-- ObjectVersion queries — immutable history rows. Populated by the
-- promotion path when the parent bucket has versioning_enabled = true.

-- name: InsertObjectVersion :exec
INSERT INTO object_versions (
    version_id, object_id, is_delete_marker, s3_key,
    size_bytes, etag, checksum_algorithm, checksum,
    content_type, metadata, tags,
    lock_mode, lock_retain_until, legal_hold
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetObjectVersion :one
SELECT version_id, object_id, is_delete_marker, s3_key,
       size_bytes, etag, checksum_algorithm, checksum,
       content_type, metadata, tags,
       lock_mode, lock_retain_until, legal_hold,
       created_at
FROM object_versions
WHERE version_id = $1;

-- name: ListObjectVersions :many
-- Newest first. Cursor: (created_at, version_id).
SELECT version_id, object_id, is_delete_marker, s3_key,
       size_bytes, etag, checksum_algorithm, checksum,
       content_type, metadata, tags,
       lock_mode, lock_retain_until, legal_hold,
       created_at
FROM object_versions
WHERE object_id = $1
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR created_at < sqlc.narg('after_created_at')::timestamptz
       OR (created_at = sqlc.narg('after_created_at')::timestamptz
           AND version_id < sqlc.arg('after_id')::uuid))
ORDER BY created_at DESC, version_id DESC
LIMIT sqlc.arg('page_size');

-- name: GetCurrentVersionID :one
-- Reads the pointer the `objects` row carries.
SELECT current_version_id
FROM objects
WHERE object_id = $1;

-- name: SetCurrentVersionID :exec
UPDATE objects
SET current_version_id = $2
WHERE object_id = $1;
