-- +goose Up

-- ─── api_tokens: HMAC-SHA256 lookup digest + RLS restored ───────────────────
--
-- Two coupled changes:
--
-- 1. HMAC lookup (replaces argon2id). Verification used to load rows by an
--    8-char `prefix` and argon2id-compare each candidate (~50ms + 64 MiB per
--    request, no cache). The token body is 32 bytes of CSPRNG output, so there
--    is no brute-force surface that a slow KDF protects against — a keyed FAST
--    hash is sufficient and lets us index the digest for an O(1) exact-match
--    lookup. `token_hmac` holds HMAC-SHA256(server_key, token); the server key
--    (config api_token.hmac_key) is a pepper, so a stolen DB row can't be
--    turned into a working token. token_hash (legacy argon2 PHC) is kept but
--    made NULLable — HMAC-issued rows leave it NULL; a later migration can drop
--    it once no argon2 rows remain.
--
-- 2. RLS restored (migration 059 disabled it). api_tokens is verified BEFORE a
--    tenant context exists (the token IS what establishes the tenant), so the
--    plain tenant_isolation policy filtered the pre-auth lookup to zero rows —
--    that is why 059 turned RLS off. The correct fix keeps RLS ON for writes
--    (create/revoke/list all run under the caller's tenant via the pool's
--    PrepareConn hook, so tenant_isolation's WITH CHECK isolates them) and adds
--    a permissive SELECT policy so ONLY the read path — the unavoidable
--    tenant-less verify lookup — is open. Reads expose nothing to clients: the
--    verifier returns no rows to callers, and the admin ListByTenant query
--    filters by tenant_id in SQL. TouchLastUsed sets the tenant GUC itself.

-- +goose StatementBegin
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS token_hmac bytea;
ALTER TABLE api_tokens ALTER COLUMN token_hash DROP NOT NULL;

-- Exact-match verification index. Partial (token_hmac IS NOT NULL) so legacy
-- argon2 rows with a NULL digest don't collide on the unique constraint.
CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_hmac_uniq
    ON api_tokens (token_hmac)
    WHERE token_hmac IS NOT NULL;

-- Restore tenant isolation for writes.
ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON api_tokens;
CREATE POLICY tenant_isolation ON api_tokens
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- Pre-auth verify lookup: the token establishes the tenant, so this SELECT
-- runs with no paladin.tenant_id GUC. A permissive SELECT policy lets FindByDigest
-- read the row; writes remain gated by tenant_isolation above (a SELECT-only
-- policy contributes no USING clause to INSERT/UPDATE/DELETE).
DROP POLICY IF EXISTS api_tokens_preauth_read ON api_tokens;
CREATE POLICY api_tokens_preauth_read ON api_tokens
    FOR SELECT
    USING (true);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS api_tokens_preauth_read ON api_tokens;
DROP POLICY IF EXISTS tenant_isolation ON api_tokens;
ALTER TABLE api_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE api_tokens DISABLE ROW LEVEL SECURITY;

DROP INDEX IF EXISTS api_tokens_hmac_uniq;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS token_hmac;
-- token_hash left NULLable; re-tightening would fail on any HMAC-issued row.
-- +goose StatementEnd
