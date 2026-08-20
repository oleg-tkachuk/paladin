-- ObjectVersion queries — immutable history rows. Populated by the
-- promotion path when the parent bucket has versioning_enabled = true.

-- name: InsertObjectVersion :exec
INSERT INTO object_versions (
    id, object_id, is_delete_marker, storage_path,
    size_bytes, etag, checksum_algorithm, checksum,
    content_type, metadata, tags
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: UpsertObjectLock :exec
-- Object Lock is its own row (ADR-0013). Retention is set after the version
-- exists, and the DELETE trigger on object_locks is what refuses to release it
-- early — so this is the only write path that can put a version under lock.
INSERT INTO object_locks (tenant_id, version_id, mode, retain_until, legal_hold)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (version_id) DO UPDATE SET
    mode         = EXCLUDED.mode,
    retain_until = EXCLUDED.retain_until,
    legal_hold   = EXCLUDED.legal_hold,
    updated_at   = now();

-- name: GetObjectVersion :one
SELECT sqlc.embed(v),
       l.mode AS lock_mode, l.retain_until AS lock_retain_until,
       COALESCE(l.legal_hold, false) AS legal_hold,
       v.created_at
FROM object_versions v
LEFT JOIN object_locks l ON l.version_id = v.id
WHERE v.id = $1;

-- name: ListObjectVersions :many
-- Newest first. Cursor: (created_at, id).
SELECT sqlc.embed(v),
       l.mode AS lock_mode, l.retain_until AS lock_retain_until,
       COALESCE(l.legal_hold, false) AS legal_hold,
       v.created_at
FROM object_versions v
LEFT JOIN object_locks l ON l.version_id = v.id
WHERE v.object_id = $1
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR created_at < sqlc.narg('after_created_at')::timestamptz
       OR (created_at = sqlc.narg('after_created_at')::timestamptz
           AND id < sqlc.arg('after_id')::uuid))
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('page_size');

-- name: GetCurrentVersionID :one
-- Reads the pointer the `objects` row carries.
SELECT current_version_id
FROM objects
WHERE id = $1;

-- name: SetCurrentVersionID :exec
UPDATE objects
SET current_version_id = $2
WHERE id = $1;
