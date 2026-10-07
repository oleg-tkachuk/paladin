-- ObjectVersion queries — immutable history rows. Populated by the
-- promotion path when the parent bucket has versioning_enabled = true.

-- name: InsertObjectVersion :exec
INSERT INTO object_versions (
    id, object_id, is_delete_marker, storage_path,
    size_bytes, etag, checksum_algorithm, checksum, checksum_part_size_bytes,
    content_type, metadata, tags
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: SetObjectRetention :one
-- Applies or extends a retention window on a version (ADR-0013).
--
-- The rules live in the WHERE clause rather than in Go, because a read in the
-- handler followed by a write here is a race: two concurrent calls could each
-- read a two-year COMPLIANCE window and each decide their one-year write is
-- fine. Expressed as a conditional upsert, the second one loses.
--
-- What the clause says, in order:
--
--   * a first lock on this version is always allowed;
--   * extending is allowed, provided the mode does not weaken with it —
--     a longer GOVERNANCE window is not an acceptable replacement for a
--     shorter COMPLIANCE one, which is the hole the first version of this
--     clause had;
--   * GOVERNANCE → COMPLIANCE is a tightening and needs no extension;
--   * COMPLIANCE never weakens — not shorter, not downgraded, not by a
--     platform admin. That is the property the mode exists for;
--   * an active GOVERNANCE window weakens only when the caller passes the
--     bypass flag, which the handler grants on a role.
--
-- An expired window is not "active": once retain_until has passed the row
-- holds nothing, so any new window may replace it. Returns zero rows when the
-- write is refused, which the adapter maps to ErrRetentionShortened.
INSERT INTO object_locks AS ol (tenant_id, version_id, mode, retain_until, legal_hold)
VALUES ($1, $2, sqlc.arg('mode')::object_lock_mode, sqlc.arg('retain_until')::timestamptz, false)
ON CONFLICT (version_id) DO UPDATE SET
    mode         = EXCLUDED.mode,
    retain_until = EXCLUDED.retain_until,
    updated_at   = now()
WHERE ol.retain_until IS NULL
   OR ol.retain_until <= now()
   OR (EXCLUDED.retain_until >= ol.retain_until
       AND NOT (ol.mode = 'COMPLIANCE' AND EXCLUDED.mode <> 'COMPLIANCE'))
   OR (ol.mode = 'GOVERNANCE'
       AND sqlc.arg('bypass_governance')::boolean)
RETURNING mode, retain_until, legal_hold;

-- name: SetObjectLegalHold :one
-- A legal hold is independent of retention: it can be turned on and off
-- freely by anyone the handler authorises, and while on it blocks deletion
-- regardless of any window. It is deliberately NOT subject to the GOVERNANCE
-- bypass — a hold exists to survive exactly the person with the strongest
-- role.
--
-- Turning a hold off leaves the row in place with legal_hold = false. It used
-- to have to delete the row when no retention remained, because a row
-- asserting nothing violated a CHECK — but the delete then hit the retention
-- trigger, which refuses to drop a row under hold, so a bare hold could never
-- be lifted at all. 007 drops that CHECK: a released lock is a valid row, and
-- its timestamps are the record of when the hold was placed and lifted.
INSERT INTO object_locks AS ol (tenant_id, version_id, mode, retain_until, legal_hold)
VALUES ($1, $2, NULL, NULL, sqlc.arg('legal_hold')::boolean)
ON CONFLICT (version_id) DO UPDATE SET
    legal_hold = EXCLUDED.legal_hold,
    updated_at = now()
RETURNING mode, retain_until, legal_hold;

-- name: GetObjectLockByVersion :one
SELECT mode, retain_until, legal_hold
FROM object_locks
WHERE version_id = $1;

-- name: ApplyBucketDefaultLock :exec
-- Puts a freshly promoted version under the parent bucket's default retention
-- (ADR-0013). Skipped entirely when the bucket has no default, and never
-- overwrites an existing row — an explicit SetObjectRetention that arrived
-- first outranks a default.
INSERT INTO object_locks (tenant_id, version_id, mode, retain_until, legal_hold)
VALUES ($1, $2, sqlc.arg('mode')::object_lock_mode,
        now() + make_interval(secs => sqlc.arg('retention_seconds')::bigint), false)
ON CONFLICT (version_id) DO NOTHING;

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
  -- Every column qualified with v.: the LEFT JOIN brings object_locks into
  -- scope, which also has created_at and id, so the bare names were ambiguous
  -- and Postgres refused the statement with 42702. sqlc accepted it — it
  -- checks names and shapes, not resolution — so ListObjectVersions failed on
  -- every call rather than at build time.
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR v.created_at < sqlc.narg('after_created_at')::timestamptz
       OR (v.created_at = sqlc.narg('after_created_at')::timestamptz
           AND v.id < sqlc.arg('after_id')::uuid))
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
