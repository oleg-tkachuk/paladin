-- name: CreateEventSubscription :exec
INSERT INTO event_subscriptions (
    subscription_id, tenant_id, cel_filter, sink_kind, sink_config, disabled
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetEventSubscription :one
SELECT subscription_id, tenant_id, cel_filter, sink_kind, sink_config,
       disabled, resource_version, created_at, updated_at
FROM event_subscriptions
WHERE subscription_id = $1;

-- name: ListEventSubscriptions :many
-- Cursor pagination with optional tenant filter. The after_id branch
-- MUST be wrapped in `IS NULL OR …` — first-page callers pass
-- uuid.Nil, which pgUUID() maps to SQL NULL, and a bare
-- `subscription_id > NULL` yields zero rows. Keep this shape on every
-- cursor query in the package.
SELECT subscription_id, tenant_id, cel_filter, sink_kind, sink_config,
       disabled, resource_version, created_at, updated_at
FROM event_subscriptions
WHERE (sqlc.narg('tenant_id')::uuid IS NULL OR tenant_id = sqlc.narg('tenant_id')::uuid)
  AND (sqlc.narg('after_id')::uuid IS NULL
       OR subscription_id > sqlc.narg('after_id')::uuid)
ORDER BY subscription_id ASC
LIMIT sqlc.arg('page_size')::int;

-- name: UpdateEventSubscription :execrows
UPDATE event_subscriptions
SET cel_filter  = COALESCE(sqlc.narg('cel_filter'), cel_filter),
    sink_kind   = COALESCE(sqlc.narg('sink_kind'), sink_kind),
    sink_config = COALESCE(sqlc.narg('sink_config'), sink_config),
    disabled    = COALESCE(sqlc.narg('disabled'), disabled)
WHERE subscription_id = sqlc.arg('subscription_id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteEventSubscription :execrows
DELETE FROM event_subscriptions
WHERE subscription_id = sqlc.arg('subscription_id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);
