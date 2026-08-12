-- +goose Up

-- Drop the legacy argon2 token_hash column. api_tokens verification moved to
-- HMAC-SHA256 (token_hmac, migration 060); token_hash has been unread since —
-- Insert stopped writing it and every SELECT dropped it. Migration 060 made it
-- NULLable; this removes it entirely now that no code path references it.

-- +goose StatementBegin
ALTER TABLE api_tokens DROP COLUMN IF EXISTS token_hash;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restore the column (nullable — the argon2 verifier is gone, so nothing
-- repopulates it; kept only so the migration is reversible).
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS token_hash text;
-- +goose StatementEnd
