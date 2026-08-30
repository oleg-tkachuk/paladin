-- +goose Up
-- +goose StatementBegin

-- The last of the four, and it needed no special case after all.
--
-- BACKLOG carried this table as an open threat-model question: seven of its
-- ten writes were classified pre-session, so it looked to need a pre-auth
-- WRITE exemption — a connection with no tenant permitted to insert a row for
-- any tenant. That would have inverted the property the rest of the schema
-- depends on, where a missing GUC yields zero rows and surfaces the misconfig
-- immediately. Fail-open, in the one table where that is worst.
--
-- The classification was wrong. Those writes lacked a tenant on the CONTEXT,
-- never in hand:
--
--   * mintPair inserts a row whose own TenantID field is u.TenantID — at
--     login the insert happens AFTER the password is verified, so the user is
--     known;
--   * rotation reads the presented token before superseding it, so
--     stored.TenantID is available;
--   * logout decodes the tenant out of the token itself — parseRefresh has
--     always returned it, and the call site discarded it into `_`;
--   * reuse detection takes tenantID as a parameter, in both the IAM and
--     OAuth handlers.
--
-- So all seven are now pinned with WithActingTenant, exactly as `users` was
-- in 017, and the write side needs no exemption at all.
--
-- What remains is a pre-auth READ, which is genuine: Get(jti) must read the
-- row to learn its tenant, since the jti is all the caller presents. That is
-- the shape api_tokens has had since 002 and users since 017, and it gives up
-- nothing here — the table stores no token material. Its columns are id,
-- user_id, tenant_id, family_id, three timestamps and two flags; the token is
-- a signed JWT that never lands in a row. Reading someone else's row requires
-- their jti, which requires their token, at which point the row is the least
-- of it.

ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON refresh_tokens FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

CREATE POLICY refresh_tokens_preauth_read ON refresh_tokens FOR SELECT
    USING (paladin_session_tenant_id() IS NULL);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS refresh_tokens_preauth_read ON refresh_tokens;
DROP POLICY IF EXISTS tenant_isolation ON refresh_tokens;
ALTER TABLE refresh_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
