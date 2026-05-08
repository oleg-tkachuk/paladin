-- +goose Up
-- +goose StatementBegin

-- RLS coverage extension: write-side enforcement on tables that
-- need cross-tenant SELECT but tenant-stamped INSERT.
--
-- Two tables fit this shape:
--
--   audit_log   — compliance + security review reads cross-tenant
--                 (the "platform.admin lists every recent failed
--                 auth across every tenant" use case). But every
--                 INSERT must stamp the actor's tenant — a buggy
--                 handler that wrote with the wrong actor_tenant_id
--                 would taint downstream attribution forever.
--
--   operations  — workers run as paladin_migrate (BYPASSRLS) and
--                 consume across tenants. Same shape: SELECT free
--                 (workers need it), INSERT WITH CHECK.
--
-- Policy form: PERMISSIVE policy with no USING clause (USING is
-- the SELECT/UPDATE/DELETE filter; omitting it means "match all"),
-- and a WITH CHECK that constrains INSERT/UPDATE rows to the
-- session's tenant. The migrate role's BYPASSRLS still works
-- because RLS only kicks in for non-bypass roles.
--
-- "OR paladin_session_tenant_id() IS NULL": the GUC is unset on
-- paladin_app connections that handle login flows (no tenant context
-- yet). Allowing NULL-GUC INSERT keeps those flows working.
-- paladin_migrate bypasses RLS entirely so this branch only matters
-- for the narrow "paladin_app, pre-tenant" set — bootstrap audit
-- entries, login attempts, etc.
-- audit_log: actor_tenant_id is nullable, so we accept NULL rows
-- (system-issued, no actor) too.

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_write_isolation ON audit_log
    FOR ALL
    TO PUBLIC
    USING (true)  -- SELECT/UPDATE/DELETE: free. Compliance reads need it.
    WITH CHECK (
        paladin_session_tenant_id() IS NULL
        OR actor_tenant_id IS NULL
        OR actor_tenant_id = paladin_session_tenant_id()
    );

ALTER TABLE operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE operations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_write_isolation ON operations
    FOR ALL
    TO PUBLIC
    USING (true)  -- workers consume across tenants under BYPASSRLS;
                  -- handlers SELECTing their own tenant's ops would
                  -- need a stricter policy that we'll add when an
                  -- explicit tenant-scoped operations RPC lands.
    WITH CHECK (
        paladin_session_tenant_id() IS NULL
        OR tenant_id = paladin_session_tenant_id()
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS tenant_write_isolation ON operations;
ALTER TABLE operations DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_write_isolation ON audit_log;
ALTER TABLE audit_log DISABLE ROW LEVEL SECURITY;

-- +goose StatementEnd
