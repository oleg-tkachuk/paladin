-- +goose Up
-- +goose StatementBegin

-- Two of the four tables 014 tried to cover at once. The other two are not
-- here on purpose: `users` and `refresh_tokens` have pre-session WRITE paths
-- (login updates last_login_at and inserts a refresh token before any tenant
-- is known) and a SELECT-only exemption cannot admit them — which is exactly
-- how 014 broke every login. BACKLOG carries the full write surface.
--
-- These two are different, and the difference was checked rather than assumed:
--
--   user_settings — every write is keyed on the caller's own tenant. The three
--   ADMIN paths (GetForUser, ListByTenant, DeleteForUser) do reach another
--   tenant's rows for a platform admin, so they now run under
--   auth.WithActingTenant(target), which makes them ordinary scoped access to
--   that tenant rather than a widening. No pre-auth path exists at all: nobody
--   reads settings before authenticating.
--
--   tenant_default_bindings — the admin plane writes on behalf of another
--   tenant, and already ran under WithActingTenant before this migration. Its
--   one cross-tenant READER is the /stats census, which counts tenants with no
--   binding across the whole platform; that read now sets the cross-tenant
--   flag. Without it the subquery would see only the caller's binding and
--   every other tenant would be counted as unbound — a wrong number on a page,
--   with no error anywhere to notice.
--
-- The policy shape is the house one: USING admits paladin_session_cross_tenant()
-- so a platform-admin read can span tenants; WITH CHECK never does, so a write
-- stays pinned to exactly one tenant no matter how the flag is set.

ALTER TABLE user_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_settings FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

ALTER TABLE tenant_default_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_default_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_default_bindings FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS tenant_isolation ON user_settings;
ALTER TABLE user_settings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE user_settings DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON tenant_default_bindings;
ALTER TABLE tenant_default_bindings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_default_bindings DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
