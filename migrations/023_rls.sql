-- +goose Up
-- +goose StatementBegin

-- Row-level security as defence-in-depth.
--
-- The application layer (Cedar + handler-level guards) already
-- enforces tenant isolation. RLS is the second line: if a future
-- bug in the app layer leaks tenant context, Postgres rejects the
-- query instead of returning the wrong tenant's rows.
--
-- Approach:
--
--   1. Per-table policies key on a session-local GUC `paladin.tenant_id`
--      that the runtime sets via SET LOCAL on each transaction
--      (driven by middleware in the runtime; see internal/store/
--      postgres/rls.go).
--
--   2. The runtime role `paladin_app` is RESTRICTED to RLS — Postgres
--      rejects un-set-GUC queries with "policy violation". Worker /
--      migrate paths that legitimately span tenants run as
--      `paladin_migrate` (BYPASSRLS) or open a per-job connection that
--      sets the GUC explicitly.
--
--   3. Policy expression: `tenant_id::text = current_setting(
--      'paladin.tenant_id', true)`. The `true` second arg returns NULL
--      when the GUC isn't set (rather than raising); paired with
--      `IS NOT NULL` in the policy, that means "no GUC ⇒ no rows".
--      Closed-by-default: misconfigured runtime sees zero rows and
--      surfaces the issue immediately rather than silently leaking
--      cross-tenant.
--
-- Tables covered: objects, object_tags, multipart_uploads, quotas,
--                 event_subscriptions, capability_records,
--                 api_tokens, capability_usage.
--
-- Tables NOT covered:
--   - tenants, storage_backends, buckets, object_keys —
--     platform-admin reads cross-tenant; gating these in RLS
--     would force every admin RPC to swap connections.
--   - audit_log — security/compliance reads need cross-tenant
--     visibility; tenant_id is on the row for filtering, not
--     restriction.
--   - users, refresh_tokens, api_keys (legacy iam) —
--     cross-tenant lookups happen during login (subject not
--     yet associated with a tenant).
--   - capability_revocations — verifier path on every plane;
--     dropping a row from the denylist must be visible to all
--     tenants.
--   - operations — workers across roles consume; spanning
--     tenants is the design.
--
-- Operators who want stricter RLS extend the policy set in their
-- own migration.

-- Grant BYPASSRLS to paladin_migrate. This role runs goose migrations
-- AND the worker / housekeeping pods that legitimately span tenants
-- (audit purger, lifecycle worker, reaper). The runtime DML role
-- `paladin_app` does NOT have BYPASSRLS.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_migrate') THEN
        ALTER ROLE paladin_migrate BYPASSRLS;
    END IF;
END $$;

-- Helper: a single SQL function the policies share. Returns the
-- session-local tenant id as UUID, or NULL when the GUC is unset.
-- IMMUTABLE LEAKPROOF so the planner can fold it into index lookups.
CREATE OR REPLACE FUNCTION paladin_session_tenant_id() RETURNS uuid
    LANGUAGE sql
    STABLE
    PARALLEL SAFE
AS $$
    SELECT NULLIF(current_setting('paladin.tenant_id', true), '')::uuid
$$;

-- ─── Policy template ───────────────────────────────────────────────
-- For each table:
--   1. ALTER TABLE … ENABLE ROW LEVEL SECURITY
--   2. ALTER TABLE … FORCE ROW LEVEL SECURITY
--      (applies to table OWNER too — without this, a connection
--      that happens to own the table bypasses RLS)
--   3. CREATE POLICY tenant_isolation FOR ALL USING (...)
--      WITH CHECK (...)

-- objects
ALTER TABLE objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE objects FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON objects
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- object_tags (tenant_id directly on the row)
ALTER TABLE object_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON object_tags
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- quotas
ALTER TABLE quotas ENABLE ROW LEVEL SECURITY;
ALTER TABLE quotas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quotas
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- event_subscriptions
ALTER TABLE event_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON event_subscriptions
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- capability_records (issued capabilities are per-tenant by subject;
-- the rows have `subject_tenant_id`).
ALTER TABLE capability_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_records FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capability_records
    FOR ALL
    USING (subject_tenant_id = paladin_session_tenant_id())
    WITH CHECK (subject_tenant_id = paladin_session_tenant_id());

-- api_tokens
ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_tokens
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- multipart_uploads — keyed via objects.tenant_id, not directly.
-- We can't write a tenant-aware policy without a join. Pragmatic
-- approach: gate on object_id being one the current tenant owns.
-- Postgres optimises the EXISTS subquery via the objects index.
ALTER TABLE multipart_uploads ENABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_uploads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON multipart_uploads
    FOR ALL
    USING (EXISTS (
        SELECT 1 FROM objects o
        WHERE o.object_id = multipart_uploads.object_id
          AND o.tenant_id = paladin_session_tenant_id()
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM objects o
        WHERE o.object_id = multipart_uploads.object_id
          AND o.tenant_id = paladin_session_tenant_id()
    ));

-- multipart_parts — same indirection one level deeper.
ALTER TABLE multipart_parts ENABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_parts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON multipart_parts
    FOR ALL
    USING (EXISTS (
        SELECT 1 FROM multipart_uploads u
        JOIN objects o ON o.object_id = u.object_id
        WHERE u.upload_id = multipart_parts.upload_id
          AND o.tenant_id = paladin_session_tenant_id()
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM multipart_uploads u
        JOIN objects o ON o.object_id = u.object_id
        WHERE u.upload_id = multipart_parts.upload_id
          AND o.tenant_id = paladin_session_tenant_id()
    ));

-- capability_usage — keyed by capability_id; we look up the parent
-- capability_records row to derive the tenant.
ALTER TABLE capability_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capability_usage
    FOR ALL
    USING (EXISTS (
        SELECT 1 FROM capability_records c
        WHERE c.id = capability_usage.capability_id
          AND c.subject_tenant_id = paladin_session_tenant_id()
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM capability_records c
        WHERE c.id = capability_usage.capability_id
          AND c.subject_tenant_id = paladin_session_tenant_id()
    ));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS tenant_isolation ON capability_usage;
ALTER TABLE capability_usage DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON multipart_parts;
ALTER TABLE multipart_parts DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON multipart_uploads;
ALTER TABLE multipart_uploads DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON api_tokens;
ALTER TABLE api_tokens DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON capability_records;
ALTER TABLE capability_records DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON event_subscriptions;
ALTER TABLE event_subscriptions DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON quotas;
ALTER TABLE quotas DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON object_tags;
ALTER TABLE object_tags DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON objects;
ALTER TABLE objects DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS paladin_session_tenant_id();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_migrate') THEN
        ALTER ROLE paladin_migrate NOBYPASSRLS;
    END IF;
END $$;

-- +goose StatementEnd
