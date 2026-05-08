-- v2 storage_backends queries — full CRUD over the now-first-class entity.

-- name: UpsertStorageBackendV2 :exec
-- Used by both Create RPC (new row) and config seeding (idempotent on re-deploy).
INSERT INTO storage_backends (
    id, kind, endpoint, region, events_enabled, events_target,
    display_name, public_endpoint, force_path_style,
    credentials_secret_ref, sse_type, sse_key_id,
    events_queue_url, events_poll_interval_ms, cedar_policy
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
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
    updated_at              = now();

-- name: GetStorageBackendV2 :one
SELECT id, kind, endpoint, region, events_enabled, events_target,
       display_name, public_endpoint, force_path_style,
       credentials_secret_ref, sse_type, sse_key_id,
       events_queue_url, events_poll_interval_ms,
       cedar_policy, cedar_policy_hash,
       resource_version, created_at, updated_at
FROM storage_backends
WHERE id = $1;

-- name: ListStorageBackends :many
SELECT id, kind, endpoint, region, events_enabled, events_target,
       display_name, public_endpoint, force_path_style,
       credentials_secret_ref, sse_type, sse_key_id,
       events_queue_url, events_poll_interval_ms,
       cedar_policy, cedar_policy_hash,
       resource_version, created_at, updated_at
FROM storage_backends
WHERE id > $1
ORDER BY id ASC
LIMIT $2;

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
WHERE id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: RotateStorageBackendCredentials :execrows
UPDATE storage_backends
SET credentials_secret_ref = $2
WHERE id = $1;

-- name: DeleteStorageBackend :execrows
DELETE FROM storage_backends
WHERE id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: CountBucketsForBackend :one
SELECT count(*)::bigint AS count
FROM buckets
WHERE backend_id = $1;
