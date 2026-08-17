-- +goose Up

-- Roles on API tokens.
--
-- An API token's principal carried scopes but never roles, so a machine caller
-- could not satisfy any role-gated policy. That was fine while every
-- role-gated RPC was operator-only, and it stopped being fine once a consumer
-- had to mint capabilities for the tenants it serves (ADR-0010): issuing
-- cross-tenant requires a role, and the only way to hold one was to log in as a
-- human user and manage a session — trading N tenant credentials for a stored
-- password.
--
-- Roles here are the narrow, purpose-built kind: `platform.capability-issuer`
-- may mint a capability for any tenant and nothing else — it cannot create or
-- delete a tenant, and it cannot mint another API token. Granting a role at
-- creation is itself gated to platform.admin in the handler, so a tenant-level
-- caller cannot promote its own token.
--
-- Default '{}' so every existing row keeps its current (role-less) authority.

-- +goose StatementBegin
ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS roles text[] NOT NULL DEFAULT '{}'::text[];
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_tokens DROP COLUMN IF EXISTS roles;
-- +goose StatementEnd
