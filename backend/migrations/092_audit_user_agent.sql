-- +goose Up

-- The User-Agent an audited call arrived with: the SDK and its version, then
-- the application's own suffix, so the log shows which client made a call.
-- NULL when the caller sent none.
--
-- Nullable with no default, so this is a catalog change only (CONVENTIONS.md).
ALTER TABLE audit_log ADD COLUMN user_agent text;

-- +goose Down
ALTER TABLE audit_log DROP COLUMN IF EXISTS user_agent;
