-- +goose Up
-- +goose StatementBegin

-- Two tables landed with a tenant_id column and no policy, and the RLS
-- coverage gate caught them (tests/integration/rls_coverage_gate_test.go).
--
-- The comment in 009 claimed a policy could not work for tenant_rate_buckets
-- because the limiter runs before any acting-tenant is established. That was
-- wrong. EnableRLS sets `paladin.tenant_id` in PrepareConn, on every connection
-- checkout, from auth.EffectiveTenant — and the limiter runs after the auth
-- interceptors, so its connection carries the tenant like any other. The
-- exemption would have been a claim nobody checked.
--
-- pending_multipart_aborts is the same shape as pending_purges, which has
-- carried this policy since the schema baseline. Both are read by workers on
-- the reaper role, which holds BYPASSRLS, so the drainers see every tenant's
-- debt while a tenant sees only its own.

-- 002 drops paladin_apply_tenant_isolation once it has applied the baseline
-- set, so the statements are spelled out here. They are the procedure's body
-- verbatim: FORCE as well as ENABLE, because migrations run as the table owner
-- and an owner bypasses its own policies otherwise; USING admits the
-- cross-tenant read flag while WITH CHECK does not, so reads can widen and
-- writes stay pinned to one tenant.

ALTER TABLE tenant_rate_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_rate_buckets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_rate_buckets FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

ALTER TABLE pending_multipart_aborts ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_multipart_aborts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pending_multipart_aborts FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS tenant_isolation ON tenant_rate_buckets;
DROP POLICY IF EXISTS tenant_isolation ON pending_multipart_aborts;
ALTER TABLE tenant_rate_buckets DISABLE ROW LEVEL SECURITY;
ALTER TABLE pending_multipart_aborts DISABLE ROW LEVEL SECURITY;

-- +goose StatementEnd
