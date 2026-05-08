-- name: CreateApiKey :exec
INSERT INTO api_keys (
    api_key_id, tenant_id, display_prefix, description, secret_hash,
    roles, scopes, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetApiKeyByID :one
SELECT api_key_id, tenant_id, display_prefix, description,
       secret_hash, secret_hash_old, secret_hash_old_until,
       roles, scopes, revoked,
       created_at, expires_at, last_used_at
FROM api_keys
WHERE api_key_id = $1;

-- name: GetApiKeyByPrefix :one
SELECT api_key_id, tenant_id, display_prefix, description,
       secret_hash, secret_hash_old, secret_hash_old_until,
       roles, scopes, revoked,
       created_at, expires_at, last_used_at
FROM api_keys
WHERE display_prefix = $1
  AND revoked = FALSE;

-- name: RevokeApiKey :exec
UPDATE api_keys
SET revoked = TRUE
WHERE api_key_id = $1;

-- name: RotateApiKeySecret :exec
UPDATE api_keys
SET secret_hash           = $2,
    secret_hash_old       = $3,
    secret_hash_old_until = $4
WHERE api_key_id = $1;

-- name: TouchApiKeyUse :exec
UPDATE api_keys
SET last_used_at = $2
WHERE api_key_id = $1;

-- name: ListExpiredApiKeys :many
-- Returns api_keys whose `expires_at` has passed and that are still active.
-- Used by the housekeeping worker to flip them to revoked.
SELECT api_key_id, tenant_id, display_prefix, description,
       secret_hash, secret_hash_old, secret_hash_old_until,
       roles, scopes, revoked,
       created_at, expires_at, last_used_at
FROM api_keys
WHERE revoked = FALSE
  AND expires_at IS NOT NULL
  AND expires_at < $1
ORDER BY expires_at ASC
LIMIT $2;

-- name: ListApiKeysByTenant :many
-- $3 is the keyset-pagination cursor; pgUUID(uuid.Nil) maps to NULL,
-- which the Go adapter passes for the first page. Without the IS NULL
-- guard, `api_key_id > NULL` evaluates to NULL → all rows filtered out
-- and the first call returns empty even when rows exist.
SELECT api_key_id, tenant_id, display_prefix, description,
       secret_hash, secret_hash_old, secret_hash_old_until,
       roles, scopes, revoked,
       created_at, expires_at, last_used_at
FROM api_keys
WHERE tenant_id = $1
  AND ($2::boolean OR revoked = FALSE)
  AND ($3::uuid IS NULL OR api_key_id > $3::uuid)
ORDER BY api_key_id ASC
LIMIT $4;
