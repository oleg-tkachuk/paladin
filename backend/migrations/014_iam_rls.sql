-- +goose Up
-- +goose StatementBegin

-- Four tables carried `tenant_id` with no policy and no note saying why.
-- Silence is not a decision: a reader cannot tell an exemption from an
-- omission, and the one time this set was audited by hand it hid a real hole
-- (a tenant could revoke another tenant's capability — 004).
--
-- The blocker was never the SQL. It was that `users` is read on three paths
-- that a tenant-scoped policy would break, and they had to be found first:
--
--   1. login, before any tenant is known — the row is what identifies it;
--   2. ListUsers with no tenant, the platform-admin cross-tenant listing;
--   3. ListMyMemberships and SwitchTenant, which read a subject's rows in
--      OTHER tenants while the session is scoped to the current one.
--
-- (1) is the api_tokens problem and takes the api_tokens answer: a second,
-- SELECT-only policy that applies exactly when no session tenant is set.
-- (2) and (3) are what `paladin.cross_tenant` exists for, and the call sites
-- now set it — the flag widens SELECT only, so a write stays pinned to one
-- tenant no matter what.
--
-- Every policy here can only ever TIGHTEN: today these tables have none, so a
-- session that carries a tenant sees everything. Nothing loosens.
--
-- The policies are spelled out rather than applied through
-- paladin_apply_tenant_isolation, which 002 drops on its way out — it is that
-- migration's local helper, not a standing API. The shape is identical:
-- USING admits the cross-tenant read flag, WITH CHECK never does, so a write
-- stays pinned to one tenant however the flag is set.

-- users ─────────────────────────────────────────────────────────────────────
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- Login reads the row before the tenant is known — GetUserBySubject with a
-- tenant hint, FindUsersBySubjectGlobal without one. Narrowed by the grant,
-- not by the policy, exactly as api_tokens_preauth_read is.
CREATE POLICY users_preauth_read ON users FOR SELECT
    USING (paladin_session_tenant_id() IS NULL);

-- refresh_tokens ────────────────────────────────────────────────────────────
-- Same shape as users, for the same reason: a refresh is presented before
-- there is a session to scope it to, and the token IS the credential.
ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON refresh_tokens FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());
CREATE POLICY refresh_tokens_preauth_read ON refresh_tokens FOR SELECT
    USING (paladin_session_tenant_id() IS NULL);

-- user_settings ─────────────────────────────────────────────────────────────
-- Read and written only after authentication, always for the caller's own
-- tenant. No pre-auth path, so no second policy.
ALTER TABLE user_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_settings FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- tenant_default_bindings ───────────────────────────────────────────────────
-- Written by the admin plane on behalf of ANOTHER tenant, which WITH CHECK
-- would refuse. The handler now runs those writes under auth.WithActingTenant,
-- which sets the session tenant to the target for the duration — the same
-- mechanism the API-token admin surface uses. So the ordinary policy is
-- correct here and no exemption is needed.
ALTER TABLE tenant_default_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_default_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_default_bindings FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS users_preauth_read ON users;
DROP POLICY IF EXISTS tenant_isolation ON users;
ALTER TABLE users NO FORCE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS refresh_tokens_preauth_read ON refresh_tokens;
DROP POLICY IF EXISTS tenant_isolation ON refresh_tokens;
ALTER TABLE refresh_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON user_settings;
ALTER TABLE user_settings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE user_settings DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON tenant_default_bindings;
ALTER TABLE tenant_default_bindings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_default_bindings DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
