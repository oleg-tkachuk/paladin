-- User-settings queries. Lazy 1:1 with users — a missing row at read time
-- means "user has never customized; serve defaults".

-- name: GetUserSettings :one
SELECT user_id, tenant_id, timezone, locale, theme, preferences,
       resource_version, created_at, updated_at
FROM user_settings
WHERE user_id = $1;

-- name: UpsertUserSettings :one
-- Insert-or-update with a single round trip. Returns the post-write row so
-- the handler can echo the bumped resource_version back to the caller.
INSERT INTO user_settings (
    user_id, tenant_id, timezone, locale, theme, preferences
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (user_id) DO UPDATE
SET timezone    = EXCLUDED.timezone,
    locale      = EXCLUDED.locale,
    theme       = EXCLUDED.theme,
    preferences = EXCLUDED.preferences
RETURNING user_id, tenant_id, timezone, locale, theme, preferences,
          resource_version, created_at, updated_at;

-- name: ListUserSettingsByTenant :many
-- Admin-side: surface configured settings across a tenant for support and
-- compliance flows ("which users opted into the dark theme?").
SELECT user_id, tenant_id, timezone, locale, theme, preferences,
       resource_version, created_at, updated_at
FROM user_settings
WHERE tenant_id = $1
ORDER BY user_id
LIMIT $2;

-- name: DeleteUserSettings :execrows
DELETE FROM user_settings WHERE user_id = $1;
