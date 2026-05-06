-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (jti, user_id, tenant_id, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetRefreshToken :one
SELECT jti, user_id, tenant_id, issued_at, expires_at, revoked
FROM refresh_tokens
WHERE jti = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked = TRUE
WHERE jti = $1;

-- name: RevokeRefreshTokensForUser :execrows
UPDATE refresh_tokens
SET revoked = TRUE
WHERE user_id = $1 AND revoked = FALSE;

-- name: PurgeExpiredRefreshTokens :execrows
-- Bounded batch (10k). Worker loops until result is 0.
DELETE FROM refresh_tokens
WHERE ctid IN (
    SELECT ctid FROM refresh_tokens
    WHERE expires_at < $1
    ORDER BY expires_at
    LIMIT 10000
);
