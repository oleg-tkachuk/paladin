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
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  AND (sqlc.narg('subject_eq')::text IS NULL OR subject = sqlc.narg('subject_eq')::text)
  AND (sqlc.narg('subject_like')::text IS NULL OR subject LIKE sqlc.narg('subject_like')::text)
  AND (sqlc.narg('display_name_eq')::text IS NULL OR display_name = sqlc.narg('display_name_eq')::text)
  AND (sqlc.narg('display_name_like')::text IS NULL OR display_name LIKE sqlc.narg('display_name_like')::text)
  AND (sqlc.narg('disabled')::bool IS NULL OR disabled = sqlc.narg('disabled')::bool)
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
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  AND (sqlc.narg('subject_eq')::text IS NULL OR subject = sqlc.narg('subject_eq')::text)
  AND (sqlc.narg('subject_like')::text IS NULL OR subject LIKE sqlc.narg('subject_like')::text)
  AND (sqlc.narg('display_name_eq')::text IS NULL OR display_name = sqlc.narg('display_name_eq')::text)
  AND (sqlc.narg('display_name_like')::text IS NULL OR display_name LIKE sqlc.narg('display_name_like')::text)
  AND (sqlc.narg('disabled')::bool IS NULL OR disabled = sqlc.narg('disabled')::bool)
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
-- planner returns.
--
-- Keyset-paged on (created_at, id) rather than unbounded. The caller passes a
-- large limit by default: a membership the switcher does not show is a tenant
-- the user cannot reach, so the page is a backstop against a pathological
-- account, not a display trim. Pass after_created_at='-infinity' for the
-- first page.
SELECT id, tenant_id, subject, display_name, password_hash,
       roles, scopes, disabled, resource_version,
       created_at, updated_at, last_login_at
FROM users
WHERE subject = $1
  AND (created_at, id) > (sqlc.arg('after_created_at')::timestamptz, sqlc.arg('after_id')::uuid)
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg('page_size');
