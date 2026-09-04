-- v2 storage_backends queries — full CRUD over the now-first-class entity.

-- name: CreateStorageBackendV2 :exec
-- The Create RPC. A plain INSERT: creating something that already exists is a
-- unique violation, which the adapter maps to ErrAlreadyExists.
--
-- It used to share the upsert below, and that made Create a blind overwrite —
-- the one thing UpdateBackend refuses to be. That RPC takes a REQUIRED
-- resource_version, with "there is no bypass on this RPC by design" written
-- over it in the proto; routing Create through ON CONFLICT DO UPDATE handed
-- out exactly that bypass. Worse than the missing OCC: the SET assigns
-- EXCLUDED wholesale, so a field the caller omitted was not left alone, it was
-- blanked. Re-running a provisioning script without `cedar_policy` erased the
-- policy and answered 200.
--
-- Keyed on name, not id: the caller knows the config key ("primary"), and the
-- uuid is generated here.
INSERT INTO storage_backends (
    name, kind, endpoint, region, events_enabled, events_target,
    display_name, public_endpoint, force_path_style,
    credentials_secret_ref, sse_type, sse_key_id,
    events_queue_url, events_poll_interval_ms, cedar_policy, provider
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: UpsertStorageBackendV2 :exec
-- Config seeding only (bootstrap/backends.go), where upsert is the right
-- shape: it reconciles the YAML against the row on every boot, so the write
-- has to handle "absent" and "drifted" alike. The Create RPC no longer shares
-- it — two callers, two needs, and one query cannot serve both without giving
-- the API a silent overwrite.
--
-- `enabled` is deliberately absent from the SET, which is what keeps a backend
-- an operator disabled from coming back enabled after a restart.
--
-- Keyed on name, not id: the caller knows the config key ("primary"), and the
-- uuid is generated here. ON CONFLICT (name) makes re-seeding idempotent.
INSERT INTO storage_backends (
    name, kind, endpoint, region, events_enabled, events_target,
    display_name, public_endpoint, force_path_style,
    credentials_secret_ref, sse_type, sse_key_id,
    events_queue_url, events_poll_interval_ms, cedar_policy, provider
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (name) DO UPDATE SET
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
SELECT storage_backends.id, storage_backends.name, kind, endpoint, region, events_enabled, events_target,
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
WHERE storage_backends.name = $1;

-- name: ListStorageBackends :many
-- Cursor pagination on the backend NAME, which is what the domain calls
-- BackendID and what the caller round-trips as the page token. The surrogate
-- `id` uuid is not usable here: its ordering is meaningless to a reader, and
-- comparing it to the text cursor is a type error — Postgres rejects
-- `uuid > text` outright. `id` is also ambiguous once the health table is
-- joined, so every column here is qualified.
--
-- The IS-NULL guard is mandatory: callers may pass an empty/NULL cursor on
-- the first page, and a bare `name > NULL` evaluates to NULL → zero rows
-- (the same trap that bit ListUsersByTenant). Keep the
-- `sqlc.narg(after_id) IS NULL OR …` shape on every cursor query here.
SELECT storage_backends.id, storage_backends.name, kind, endpoint, region, events_enabled, events_target,
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
       OR storage_backends.name > sqlc.narg('after_id')::text)
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  AND (sqlc.narg('name_eq')::text IS NULL OR storage_backends.name = sqlc.narg('name_eq')::text)
  AND (sqlc.narg('name_like')::text IS NULL OR storage_backends.name LIKE sqlc.narg('name_like')::text)
  AND (sqlc.narg('display_name_eq')::text IS NULL OR display_name = sqlc.narg('display_name_eq')::text)
  AND (sqlc.narg('display_name_like')::text IS NULL OR display_name LIKE sqlc.narg('display_name_like')::text)
  -- The derived `search` field, spelled to match cel.SearchText EXACTLY.
  -- ASCII-only folding via COLLATE "C" on both columns: Go's strings.ToLower
  -- and Postgres lower() are two Unicode implementations and may disagree, and
  -- a disagreement here drops a row the authoritative CEL pass accepts. See
  -- internal/filter/cel/searchtext.go.
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(storage_backends.name COLLATE "C") || chr(10) || lower(coalesce(display_name, '') COLLATE "C")
          || chr(10) || lower(coalesce(region, '') COLLATE "C")
          || chr(10) || lower(coalesce(endpoint, '') COLLATE "C")
          LIKE sqlc.narg('search_like')::text)
  AND (sqlc.narg('provider_eq')::text IS NULL OR provider = sqlc.narg('provider_eq')::text)
  AND (sqlc.narg('region_eq')::text IS NULL OR region = sqlc.narg('region_eq')::text)
  AND (sqlc.narg('enabled')::bool IS NULL OR enabled = sqlc.narg('enabled')::bool)
  AND (sqlc.narg('read_only')::bool IS NULL OR read_only = sqlc.narg('read_only')::bool)
  AND (sqlc.narg('maintenance')::bool IS NULL OR maintenance = sqlc.narg('maintenance')::bool)
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR storage_backends.created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR storage_backends.created_at <= sqlc.narg('created_at_lte')::timestamptz)
ORDER BY storage_backends.name ASC
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
WHERE storage_backends.name = sqlc.arg('name')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendEnabled :execrows
-- Flip the enable/disable state. OCC via resource_version (the
-- trg_storage_backends_bump_rv BEFORE UPDATE trigger bumps the version).
-- enabled is intentionally NOT part of UpsertStorageBackendV2 — bootstrap
-- config-mirror must never touch this operator-managed column.
UPDATE storage_backends
SET enabled = sqlc.arg('enabled')
WHERE storage_backends.name = sqlc.arg('name')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendReadOnly :execrows
-- Flip the read-only (drain) state. Same OCC + operator-managed contract as
-- SetStorageBackendEnabled; also not part of the bootstrap config-mirror.
UPDATE storage_backends
SET read_only = sqlc.arg('read_only')
WHERE storage_backends.name = sqlc.arg('name')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetStorageBackendMaintenance :execrows
-- Flip the operator-set maintenance flag (the schema baseline (001_initial_schema.sql)). Same OCC +
-- operator-managed contract as the enable/read-only setters; advisory only.
UPDATE storage_backends
SET maintenance = sqlc.arg('maintenance')
WHERE storage_backends.name = sqlc.arg('name')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: UpsertStorageBackendHealth :exec
-- Keyed by backend NAME: callers are health probes that know the config key.
-- Record the outcome of a TestBackend probe (the schema baseline (001_initial_schema.sql)). DERIVED, advisory
-- state in its own 1:1 table — writing it does NOT touch storage_backends, so
-- it never fires the bump_rv trigger (no resource_version / updated_at churn)
-- and TestBackend stays read-only w.r.t. the config row. Last-writer-wins.
INSERT INTO storage_backend_health (backend_id, status, message, checked_at)
SELECT sb.id, sqlc.arg('status'), sqlc.arg('message'), sqlc.arg('checked_at')
FROM storage_backends sb WHERE sb.name = sqlc.arg('backend_name')
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
WHERE storage_backends.name = $1;

-- name: DeleteStorageBackend :execrows
DELETE FROM storage_backends
WHERE storage_backends.name = sqlc.arg('name')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: CountBucketsForBackend :one
SELECT count(*)::bigint AS count
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = $1;
