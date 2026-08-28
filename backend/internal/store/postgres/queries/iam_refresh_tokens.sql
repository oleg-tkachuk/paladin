-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, tenant_id, family_id, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetRefreshToken :one
SELECT id, user_id, tenant_id, family_id, issued_at, expires_at, revoked, superseded_at
FROM refresh_tokens
WHERE id = $1;

-- name: RevokeRefreshToken :exec
-- Revocation FOR CAUSE — logout. Leaves superseded_at NULL, so this token is
-- never mistaken for one that was merely rotated.
UPDATE refresh_tokens
SET revoked = TRUE
WHERE id = $1;

-- name: SupersedeRefreshToken :exec
-- Rotation: the holder traded this token for a successor. Distinct from
-- RevokeRefreshToken so a superseded token can be tolerated briefly (the
-- holder is racing its own rotation) while a revoked one never is.
UPDATE refresh_tokens
SET revoked = TRUE, superseded_at = now()
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
