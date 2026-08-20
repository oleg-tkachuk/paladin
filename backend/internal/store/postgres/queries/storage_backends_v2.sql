-- v2 storage_backends queries — full CRUD over the now-first-class entity.

-- name: UpsertStorageBackendV2 :exec
-- Used by both Create RPC (new row) and config seeding (idempotent on re-deploy).
INSERT INTO storage_backends (
    id, kind, endpoint, region, events_enabled, events_target,
    display_name, public_endpoint, force_path_style,
    credentials_secret_ref, sse_type, sse_key_id,
    events_queue_url, events_poll_interval_ms, cedar_policy, provider
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (id) DO UPDATE SET
    kind                    = EXCLUDED.kind,
    endpoint                = EXCLUDED.endpoint,
    region                  = EXCLUDED.region,
    events_enabled          = EXCLUDED.events_enabled,
    events_target           = EXCLUDED.events_target,
    display_name            = EXCLUDED.display_name,
    public_endpoint         = EXCLUDED.public_endpoint,
    force_path_style        = EXCLUDED.force_path_style,
    credentials_secret_ref  = EXCLUDED.credentials_secret_ref,
    sse_type                = EXCLUDED.sse_type,
    sse_key_id              = EXCLUDED.sse_key_id,
    events_queue_url        = EXCLUDED.events_queue_url,
    events_poll_interval_ms = EXCLUDED.events_poll_interval_ms,
    cedar_policy            = EXCLUDED.cedar_policy,
    provider                = EXCLUDED.provider,
    updated_at              = now();

-- name: GetStorageBackendV2 :one
SELECT storage_backends.id, kind, endpoint, region, events_enabled, events_target,
       display_name, public_endpoint, force_path_style,
       credentials_secret_ref, sse_type, sse_key_id,
       events_queue_url, events_poll_interval_ms,
       cedar_policy, cedar_policy_hash, enabled, read_only, maintenance, provider,
       COALESCE(h.status, 'unknown') AS health_status,
       COALESCE(h.message, '') AS health_message,
       h.checked_at AS health_checked_at,
       previous_credentials_secret_ref, previous_credentials_valid_until,
       resource_version, created_at, updated_at
FROM storage_backends
LEFT JOIN storage_backend_health h ON h.backend_id = storage_backends.id
WHERE storage_backends.id = $1;

-- name: ListStorageBackends :many
-- Cursor pagination. The IS-NULL guard is mandatory: callers may pass
-- an empty/NULL cursor on the first page, and a bare `id > NULL`
-- evaluates to NULL → zero rows (the same trap that bit
-- ListUsersByTenant). Keep the `sqlc.narg(after_id) IS NULL OR …`
-- shape on every cursor query in this package.
SELECT storage_backends.id, kind, endpoint, region, events_enabled, events_target,
       display_name, public_endpoint, force_path_style,
       credentials_secret_ref, sse_type, sse_key_id,
       events_queue_url, events_poll_interval_ms,
       cedar_policy, cedar_policy_hash, enabled, read_only, maintenance, provider,
       COALESCE(h.status, 'unknown') AS health_status,
       COALESCE(h.message, '') AS health_message,
       h.checked_at AS health_checked_at,
       previous_credentials_secret_ref, previous_credentials_valid_until,
       resource_version, created_at, updated_at
FROM storage_backends
LEFT JOIN storage_backend_health h ON h.backend_id = storage_backends.id
WHERE (sqlc.narg('after_id')::text IS NULL
       OR id > sqlc.narg('after_id')::text)
ORDER BY storage_backends.id ASC
LIMIT sqlc.arg('page_size')::int;

-- name: UpdateStorageBackend :execrows
UPDATE storage_backends
SET display_name            = COALESCE(sqlc.narg('display_name'), display_name),
    endpoint                = COALESCE(sqlc.narg('endpoint'), endpoint),
    public_endpoint         = COALESCE(sqlc.narg('public_endpoint'), public_endpoint),
    region                  = COALESCE(sqlc.narg('region'), region),
    force_path_style        = COALESCE(sqlc.narg('force_path_style'), force_path_style),
    credentials_secret_ref  = COALESCE(sqlc.narg('credentials_secret_ref'), credentials_secret_ref),
    sse_type                = COALESCE(sqlc.narg('sse_type'), sse_type),
    sse_key_id              = COALESCE(sqlc.narg('sse_key_id'), sse_key_id),
    events_enabled          = COALESCE(sqlc.narg('events_enabled'), events_enabled),
    events_target           = COALESCE(sqlc.narg('events_target'), events_target),
    events_queue_url        = COALESCE(sqlc.narg('events_queue_url'), events_queue_url),
    events_poll_interval_ms = COALESCE(sqlc.narg('events_poll_interval_ms'), events_poll_interval_ms),
    cedar_policy            = COALESCE(sqlc.narg('cedar_policy'), cedar_policy)
WHERE storage_backends.id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendEnabled :execrows
-- Flip the enable/disable state. OCC via resource_version (the
-- trg_storage_backends_bump_rv BEFORE UPDATE trigger bumps the version).
-- enabled is intentionally NOT part of UpsertStorageBackendV2 — bootstrap
-- config-mirror must never touch this operator-managed column.
UPDATE storage_backends
SET enabled = sqlc.arg('enabled')
WHERE storage_backends.id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendReadOnly :execrows
-- Flip the read-only (drain) state. Same OCC + operator-managed contract as
-- SetStorageBackendEnabled; also not part of the bootstrap config-mirror.
UPDATE storage_backends
SET read_only = sqlc.arg('read_only')
WHERE storage_backends.id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendMaintenance :execrows
-- Flip the operator-set maintenance flag (migration 049). Same OCC +
-- operator-managed contract as the enable/read-only setters; advisory only.
UPDATE storage_backends
SET maintenance = sqlc.arg('maintenance')
WHERE storage_backends.id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: UpsertStorageBackendHealth :exec
-- Record the outcome of a TestBackend probe (migration 048). DERIVED, advisory
-- state in its own 1:1 table — writing it does NOT touch storage_backends, so
-- it never fires the bump_rv trigger (no resource_version / updated_at churn)
-- and TestBackend stays read-only w.r.t. the config row. Last-writer-wins.
INSERT INTO storage_backend_health (backend_id, status, message, checked_at)
VALUES (sqlc.arg('backend_id'), sqlc.arg('status'), sqlc.arg('message'), sqlc.arg('checked_at'))
ON CONFLICT (backend_id) DO UPDATE
SET status     = EXCLUDED.status,
    message    = EXCLUDED.message,
    checked_at = EXCLUDED.checked_at;

-- name: RotateStorageBackendCredentials :execrows
-- Dual-write rotation: stash the current ref as the previous one with a
-- validity horizon of now()+grace, then swap in the new ref. $3 is the grace
-- window in seconds; 0 clears the previous window (instant rotation).
UPDATE storage_backends
SET previous_credentials_secret_ref  = credentials_secret_ref,
    previous_credentials_valid_until = CASE
        WHEN $3::bigint > 0 THEN now() + make_interval(secs => $3::bigint)
        ELSE NULL
    END,
    credentials_secret_ref           = $2
WHERE storage_backends.id = $1;

-- name: DeleteStorageBackend :execrows
DELETE FROM storage_backends
WHERE storage_backends.id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: CountBucketsForBackend :one
SELECT count(*)::bigint AS count
FROM buckets
WHERE backend_id = $1;
