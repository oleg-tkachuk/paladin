-- +goose Up

-- ─── api_tokens is a pre-auth lookup table: it must NOT be under RLS ─────────
--
-- Bug: migration 023 placed `api_tokens` under FORCE ROW LEVEL SECURITY with
-- the standard `tenant_isolation` policy `USING (tenant_id = paladin_session_tenant_id())`.
-- But an `paladin_pat_…` bearer token is verified BEFORE any tenant context exists —
-- the token itself is what establishes the tenant. The data-plane verifier's
-- `FindByPrefix` SELECT therefore runs with no `paladin.tenant_id` GUC set, so under
-- `paladin_app` (no BYPASSRLS) the policy matches ZERO rows and every PAT fails
-- verification with "api_token: token not found" — a chicken-and-egg that makes
-- data-plane API-token auth impossible for ALL tenants, not just one.
--
-- This is the same pre-tenant category as `users`, `refresh_tokens` and
-- `oauth_clients` (see migration 043's rationale), and as this table's own child
-- `api_token_rate_buckets` — none of which carry RLS, precisely because their
-- lookups happen before a tenant is known. `api_tokens` was the lone anomaly.
--
-- Tenant scoping for the WRITE paths (APITokenService.Create / Revoke / List)
-- is enforced in application code from the authenticated principal's tenant —
-- identical to how `refresh_tokens` are created for a specific user without RLS.
-- Removing RLS here also lets the verifier's best-effort `TouchLastUsed` UPDATE
-- succeed (it, too, runs pre-tenant).

DROP POLICY IF EXISTS tenant_isolation ON api_tokens;
ALTER TABLE api_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE api_tokens DISABLE ROW LEVEL SECURITY;

-- +goose Down

-- Restore the (buggy) tenant-scoped RLS. This re-breaks data-plane PAT
-- verification; kept only so the migration is reversible.
ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_tokens
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());
