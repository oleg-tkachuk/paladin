-- +goose Up
-- +goose StatementBegin

-- `users`, the table 014 could not do.
--
-- What changed is not the SQL — it is that the table no longer has a
-- pre-session WRITE. 014 failed because login stamps last_login_at before any
-- tenant is on the context, and a SELECT-only exemption cannot admit an
-- UPDATE. Rather than widen the exemption to writes, the writes were pinned:
--
--   * the six admin operations (create, update, delete, grant/revoke scopes,
--     reset password) look a user up BY ID ALONE — the tenant is a property of
--     the row, not of the request — so each read is widened with the
--     cross-tenant flag and each write that follows runs under
--     WithActingTenant(row's tenant). The authorization gate between them is
--     what bounds the widened read, and it was already there.
--   * SwitchTenant stamps a row in the target tenant; pinned the same way.
--   * login stamps its own row, and by that line the credential is verified
--     and the user IS known — so it is pinned too. That was the last one.
--
-- So the only exemption left is a pre-auth READ, which login genuinely needs:
-- it has to find the row before it can know the tenant. That is the same
-- shape api_tokens has had since 002, and it is narrowed by the grant rather
-- than by the policy.
--
-- Verified against a live stack running as paladin_app, with this policy in
-- place, before it was written as a migration:
--
--   scoped to one tenant              → 1 of 3 users visible
--   cross-tenant flag (admin listing) → 3 of 3
--   no session tenant (login)         → 3 of 3, and last_login_at stamps
--   INSERT into another tenant, flag on → refused
--   full e2e suite                    → 108 passed
--
-- refresh_tokens deliberately stays exempt. Seven of its ten writes are
-- pre-session by nature — the token is presented before a session exists,
-- which is the point — so it needs a decision about a pre-auth WRITE
-- exemption, and that is a threat-model question rather than a SQL one.
-- BACKLOG carries it.

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- Login reads the row before the tenant is known: GetUserBySubject with a
-- tenant hint, FindUsersBySubjectGlobal without one. SELECT only — every
-- write on this table now carries a tenant.
CREATE POLICY users_preauth_read ON users FOR SELECT
    USING (paladin_session_tenant_id() IS NULL);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS users_preauth_read ON users;
DROP POLICY IF EXISTS tenant_isolation ON users;
ALTER TABLE users NO FORCE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
