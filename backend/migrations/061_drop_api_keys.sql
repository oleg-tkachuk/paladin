-- +goose Up

-- ─── Drop the legacy IAM api_keys system ────────────────────────────────────
--
-- The `api_keys` table (IAM ApiKeyService — CreateApiKey / RotateApiKey /
-- MintScopedToken) is fully removed. It carried 0 rows in prod and had no
-- live bearer-verification path: all API-key/token auth is unified onto the
-- `api_tokens` system (migrations 059/060). CASCADE drops the table together
-- with its indexes (idx_api_keys_tenant, idx_api_keys_prefix from 006;
-- idx_api_keys_active_expiry from 008) and the tenant FK. api_keys was never
-- placed under RLS (see 023), so there are no policies to drop.

-- +goose StatementBegin
DROP TABLE IF EXISTS api_keys CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restore the HEAD-state schema: the 006 CREATE TABLE + its two indexes,
-- plus the 008 partial expiry index and fillfactor. Recreated non-
-- CONCURRENTLY (the rolled-back table is empty and unloaded, so a plain
-- in-transaction CREATE INDEX is safe and avoids goose NO TRANSACTION).
CREATE TABLE IF NOT EXISTS api_keys (
    api_key_id        UUID PRIMARY KEY,
    tenant_id         UUID REFERENCES tenants(tenant_id) ON DELETE RESTRICT, -- NULL = platform-level
    display_prefix    TEXT NOT NULL,                    -- first 8 chars of secret, shown in UI
    description       TEXT NOT NULL DEFAULT '',
    secret_hash       BYTEA NOT NULL,
    -- During rotation the previous secret remains valid until secret_hash_old_until.
    secret_hash_old   BYTEA,
    secret_hash_old_until TIMESTAMPTZ,
    roles             JSONB NOT NULL DEFAULT '[]'::jsonb,
    scopes            JSONB NOT NULL DEFAULT '[]'::jsonb,
    revoked           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ,
    last_used_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(display_prefix);

-- From 008: partial index for the expiry reaper (revoked rows skip it).
CREATE INDEX IF NOT EXISTS idx_api_keys_active_expiry
    ON api_keys (expires_at)
    WHERE revoked = FALSE AND expires_at IS NOT NULL;

ALTER TABLE api_keys SET (fillfactor = 85);
-- +goose StatementEnd
