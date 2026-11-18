-- API tokens (internal/auth/api_token/postgres). The plaintext is never
-- stored: prefix for display, token_hmac for the O(1) lookup.

-- name: SetSessionTenantForAPIToken :exec
-- Scopes the transaction to the token's tenant, which the api_tokens
-- tenant_isolation policy checks: a platform admin minting for another
-- tenant, or the verifier stamping last_used_at before any principal exists.
SELECT set_config('paladin.tenant_id', sqlc.arg('tenant_id')::text, true);

-- name: InsertAPIToken :exec
INSERT INTO api_tokens (
    id, tenant_id, name, prefix, token_hmac,
    scopes, roles, audience, expires_at, rate_limit_rpm,
    created_by, created_at
) VALUES (
    sqlc.arg('id'), sqlc.arg('tenant_id'), sqlc.arg('name'), sqlc.arg('prefix'), sqlc.arg('token_hmac'),
    sqlc.arg('scopes'), sqlc.arg('roles'), sqlc.arg('audience'), sqlc.arg('expires_at'), sqlc.narg('rate_limit_rpm'),
    sqlc.arg('created_by'), sqlc.arg('created_at')
);

-- name: APITokenByDigest :one
-- Reached before authentication: the api_tokens permissive SELECT policy
-- admits it with no tenant set.
SELECT id, tenant_id, name, prefix,
       scopes, roles, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
FROM   api_tokens
WHERE  token_hmac = sqlc.arg('token_hmac');

-- name: GetAPIToken :one
SELECT id, tenant_id, name, prefix,
       scopes, roles, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
FROM   api_tokens
WHERE  id = sqlc.arg('id');

-- name: RevokeAPIToken :exec
-- Idempotent: a token already revoked keeps its revoked_at.
UPDATE api_tokens
SET    revoked_at = NOW()
WHERE  id = sqlc.arg('id')
  AND  revoked_at IS NULL;

-- name: TouchAPITokenLastUsed :exec
UPDATE api_tokens SET last_used_at = sqlc.arg('last_used_at') WHERE id = sqlc.arg('id');

-- name: ListAPITokensByTenant :many
-- One page of a tenant's tokens by id, after after_id when it is set.
-- Revoked and expired tokens are left out unless asked for.
SELECT id, tenant_id, name, prefix,
       scopes, roles, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
FROM   api_tokens
WHERE  tenant_id = sqlc.arg('tenant_id')
  AND  (sqlc.arg('include_revoked')::bool OR revoked_at IS NULL)
  AND  (sqlc.arg('include_expired')::bool OR expires_at > NOW())
  AND  (sqlc.narg('after_id')::uuid IS NULL OR id > sqlc.narg('after_id')::uuid)
ORDER  BY id
LIMIT  sqlc.arg('page_limit')::int;

-- name: PurgeExpiredAPITokens :execrows
-- Deletes tokens expired for longer than expired_for_us microseconds.
DELETE FROM api_tokens
WHERE expires_at < NOW() - (sqlc.arg('expired_for_us')::bigint || ' microseconds')::interval;
