-- +goose Up
-- +goose StatementBegin

-- ─── api_tokens — hashed-bearer M2M tokens ──────────────────────────────────
--
-- Service-to-service authentication primitive distinct from capability
-- JWTs. Capability tokens are short-lived, signed, agent-runtime; API
-- tokens are long-lived, hashed-bearer, service-to-service. Industry-
-- standard pattern (Hatchet / GitHub PATs / Stripe / GitLab).
--
-- Key design points:
--
--   - Token format: `paladin_pat_<base32(32 random bytes)>`. The literal
--     prefix lets secret-scanners (gitleaks / GitHub) recognise the
--     string class; the 8-char display prefix is shown in admin UIs
--     so an operator can identify a token without seeing the rest.
--
--   - token_hash holds argon2id(token) — never plaintext. A leaked DB
--     row reveals nothing usable; verification re-hashes the supplied
--     token and compares. Salt + parameters are encoded in the
--     argon2id output string (PHC format), so rotation of params
--     happens transparently per-row.
--
--   - expires_at is NOT NULL. Hatchet / GitHub got bitten by tokens
--     created with no expiry; we force a TTL ceiling at creation. The
--     application layer caps it to ≤1y for service tokens.
--
--   - revoked_at separates "revoked" from "expired" — both deny access
--     but admin tooling shows them differently and audits keep the row.
--
--   - last_used_at updates on every successful verify. Updated via
--     write-behind by the interceptor (not on every RPC) so high-QPS
--     paths don't hammer the row. Operators use it to identify stale
--     tokens for cleanup.
--
--   - scopes is a coarse-grained text[] mirror of `auth.Scope` — fine-
--     grained gating still goes through Cedar. Typical values:
--     `api:read`, `api:write`, `admin:read`, `admin:write`.
--
--   - audience is a text[] that pins the planes the token may be
--     presented to. Same shape as capability audience but the
--     interceptor lives in internal/auth/api_token, not capability.

CREATE TABLE IF NOT EXISTS api_tokens (
    id            uuid PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    name          text NOT NULL,
    -- Display prefix: first 8 chars after the `paladin_pat_` literal. Shown
    -- in admin UIs and audit log; useful for narrowing a leak hunt
    -- without exposing the whole token.
    prefix        text NOT NULL,
    -- argon2id PHC string of the full token. Compared via subtle.
    token_hash    text NOT NULL,
    scopes        text[] NOT NULL DEFAULT '{}'::text[],
    audience      text[] NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    last_used_at  timestamptz,
    created_by    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT NOW()
);

-- Verification path: load by prefix (cheap, indexed), then argon2id
-- compare against the supplied token. Indexing on prefix means we
-- never scan the table on a verify; collisions on the 8-char prefix
-- are statistically rare and resolved by the hash compare.
CREATE INDEX IF NOT EXISTS api_tokens_prefix_idx
    ON api_tokens (prefix);

-- Per-tenant listing (admin tooling): fast query by tenant_id +
-- name. Sorted by created_at for stable pagination.
CREATE INDEX IF NOT EXISTS api_tokens_tenant_idx
    ON api_tokens (tenant_id, created_at DESC);

-- Reaper sweep: drop rows past expiry + grace window. BRIN keeps the
-- index small at hundreds-of-millions scale.
CREATE INDEX IF NOT EXISTS api_tokens_expires_brin
    ON api_tokens USING brin (expires_at);

-- Uniqueness: a tenant can't have two tokens with the same name. Eases
-- "find my ingest-svc token" lookups in the admin UI without another
-- table for human-friendly aliases.
CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_tenant_name_idx
    ON api_tokens (tenant_id, name)
    WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_tokens;
-- +goose StatementEnd
