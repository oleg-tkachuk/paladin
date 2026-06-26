-- +goose Up
-- +goose StatementBegin
-- OAuth 2.1 Authorization Server storage (ADR-0009). Two tables, both
-- AS-infrastructure rather than tenant-scoped user data:
--
--   oauth_clients              — registered OAuth clients (first-party seeds
--                                + dynamically-registered, RFC 7591).
--   oauth_authorization_codes  — short-lived, single-use auth codes bound to
--                                a PKCE challenge (RFC 7636).
--
-- NO row-level security, by the same rationale migration 023 applies to
-- `users` / `refresh_tokens`: the OAuth /authorize + /token endpoints run
-- pre-tenant (raw HTTP, no paladin.tenant_id GUC set), and code/client lookups
-- happen before a tenant context exists. paladin_app gets DML automatically via
-- the ALTER DEFAULT PRIVILEGES from migration 011.

CREATE TABLE oauth_clients (
    client_id         text        PRIMARY KEY,
    client_name       text        NOT NULL DEFAULT '',
    redirect_uris     text[]      NOT NULL DEFAULT '{}',
    allowed_scopes    text[]      NOT NULL DEFAULT '{}',
    allowed_audiences text[]      NOT NULL DEFAULT '{}',
    -- bcrypt hash of the client_secret; NULL for public (PKCE-only) clients.
    secret_hash       bytea,
    is_public         boolean     NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_authorization_codes (
    -- sha256 of the issued code: a DB leak never exposes a live, redeemable
    -- code (same posture as hashed api_tokens).
    code_hash             bytea       PRIMARY KEY,
    client_id             text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id               uuid        NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    tenant_id             uuid        NOT NULL,
    redirect_uri          text        NOT NULL,
    code_challenge        text        NOT NULL,
    code_challenge_method text        NOT NULL,
    scopes                text[]      NOT NULL DEFAULT '{}',
    audience              text        NOT NULL,
    expires_at            timestamptz NOT NULL,
    -- single-use guard: set atomically on redemption; a second redeem finds
    -- it non-NULL and is rejected.
    consumed_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- Reaper support: prune expired/consumed codes by age.
CREATE INDEX idx_oauth_authorization_codes_expires_at
    ON oauth_authorization_codes (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_oauth_authorization_codes_expires_at;
DROP TABLE IF EXISTS oauth_authorization_codes;
DROP TABLE IF EXISTS oauth_clients;
-- +goose StatementEnd
