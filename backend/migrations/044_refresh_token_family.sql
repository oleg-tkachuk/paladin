-- +goose Up
-- +goose StatementBegin
-- Refresh-token families for precise reuse-detection (ADR-0009). Every login
-- starts a new family; each rotation inherits its predecessor's family_id.
-- Replaying a rotated (revoked) token then revokes only that family (the
-- compromised device/session chain) instead of every token for the user.
--
-- DEFAULT gen_random_uuid() backfills existing rows as singleton families
-- (each its own) and is a safe net for any insert that omits family_id — an
-- unspecified family just isolates that token rather than chaining it.
ALTER TABLE refresh_tokens
    ADD COLUMN family_id uuid NOT NULL DEFAULT gen_random_uuid();

CREATE INDEX idx_refresh_tokens_family ON refresh_tokens (family_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_refresh_tokens_family;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;
-- +goose StatementEnd
