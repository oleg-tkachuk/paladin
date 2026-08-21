-- name: CreateUser :exec
INSERT INTO users (
    id, tenant_id, subject, display_name, password_hash,
    roles, scopes, disabled
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetUserByID :one
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE id = $1;

-- name: GetUserBySubject :one
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE tenant_id = $1 AND subject = $2;

-- name: UpdateUser :execrows
UPDATE users
SET display_name = COALESCE(sqlc.narg(display_name), display_name),
    disabled     = COALESCE(sqlc.narg(disabled), disabled),
    roles        = COALESCE(sqlc.narg(roles), roles),
    scopes       = COALESCE(sqlc.narg(scopes), scopes),
    updated_at   = now()
WHERE id = sqlc.arg(id)
  AND (sqlc.arg(expected_version) = 0 OR resource_version = sqlc.arg(expected_version));

-- name: UpdateUserPasswordHash :exec
UPDATE users
SET password_hash = $2,
    updated_at    = now()
WHERE id = $1;

-- name: TouchUserLogin :exec
UPDATE users
SET last_login_at = $2
WHERE id = $1;

-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = sqlc.arg(id)
  AND (sqlc.arg(expected_version) = 0 OR resource_version = sqlc.arg(expected_version));

-- name: ListUsersByTenant :many
-- after_id is NULL on first page (no page token). The IS-NULL guard
-- prevents the NULL-propagation that would make a bare `id >
-- NULL` return zero rows. pgUUID() maps uuid.Nil → pgtype.UUID{
-- Valid:false} → SQL NULL, so the guard is the contract callers
-- rely on.
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE tenant_id = $1
  AND (sqlc.narg('after_id')::uuid IS NULL
       OR id > sqlc.narg('after_id')::uuid)
ORDER BY id ASC
LIMIT sqlc.arg('page_size')::int;

-- name: ListUsersAll :many
-- Cross-tenant listing for platform.admin. Same NULL-safe after_id
-- pattern as ListUsersByTenant — without the IS-NULL guard the
-- /users page renders empty even when there are rows, because the
-- adapter sends pgUUID(uuid.Nil) which maps to SQL NULL.
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE (sqlc.narg('after_id')::uuid IS NULL
       OR id > sqlc.narg('after_id')::uuid)
ORDER BY id ASC
LIMIT sqlc.arg('page_size')::int;

-- name: FindUsersBySubjectGlobal :many
-- Cross-tenant subject lookup for AuthService.Login when the caller supplied
-- no tenant hint. The handler only needs to know whether the subject is
-- unambiguous, so five rows is plenty and the cap keeps a shared subject from
-- turning every login into a full scan.
--
-- NOT for listing a user's memberships: that needs all of them, in a stable
-- order — see ListMembershipsBySubject below. The two shared this query once,
-- and the tenant switcher silently hid every membership past the fifth.
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE subject = $1
LIMIT 5;

-- name: ListMembershipsBySubject :many
-- Every tenant this subject belongs to, for the tenant switcher. Ordered by
-- creation so the list is stable across calls rather than whatever the
-- planner returns; unbounded because a membership the UI does not show is a
-- tenant the user cannot reach.
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE subject = $1
ORDER BY created_at ASC, id ASC;
