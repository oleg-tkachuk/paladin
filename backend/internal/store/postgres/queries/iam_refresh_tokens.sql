-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, tenant_id, family_id, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetRefreshToken :one
SELECT id, user_id, tenant_id, family_id, issued_at, expires_at, revoked
FROM refresh_tokens
WHERE id = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked = TRUE
WHERE id = $1;

-- name: RevokeRefreshTokensForUser :execrows
UPDATE refresh_tokens
SET revoked = TRUE
WHERE user_id = $1 AND revoked = FALSE;

-- name: RevokeRefreshTokenFamily :execrows
-- Reuse-detection (ADR-0009): revoke every still-live token in the family of
-- the given id — the compromised chain only, not all the user's sessions.
UPDATE refresh_tokens
SET revoked = TRUE
WHERE family_id = (SELECT rt.family_id FROM refresh_tokens AS rt WHERE rt.id = $1)
  AND revoked = FALSE;

-- name: PurgeExpiredRefreshTokens :execrows
-- Bounded batch (10k). Worker loops until result is 0.
DELETE FROM refresh_tokens
WHERE ctid IN (
    SELECT rt.ctid FROM refresh_tokens AS rt
    WHERE rt.expires_at < $1
    ORDER BY rt.expires_at
    LIMIT 10000
);
