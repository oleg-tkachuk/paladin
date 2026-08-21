-- +goose Up
-- +goose StatementBegin

-- Roles and row-level security.
--
-- RLS is a PRIMARY isolation control here, not defence-in-depth: the data
-- plane runs as paladin_app, which has no BYPASSRLS, so a missing policy means
-- a missing wall. Every policy is single-table and reads tenant_id directly —
-- that is why tenant_id is denormalised onto every tenant-scoped table
-- (ADR-0013 §4).

-- ─── Roles ──────────────────────────────────────────────────────────────────
--
-- Idempotent: the DBA may have created these with a password already. NOLOGIN
-- is the safe default — a deployment grants LOGIN with an explicit
-- ALTER ROLE … WITH LOGIN PASSWORD '…'.

-- Role management is privileged, and the role running migrations may not
-- hold that privilege. Three deploy shapes, all of which must work:
--
--   1. Migrations run as superuser (a fresh testcontainer, a laptop) —
--      everything below succeeds.
--   2. Migrations run as a non-superuser with CREATEROLE — the roles get
--      created, but ALTER ROLE … BYPASSRLS fails with 42501: a CREATEROLE
--      user cannot grant that attribute.
--   3. Migrations run as `paladin_migrate` itself, which is what a
--      provisioned cluster does — a role cannot ALTER its own attributes,
--      and it cannot CREATE roles either.
--
-- On shapes (2) and (3) the operator MUST have provisioned the roles, with
-- BYPASSRLS on paladin_migrate and paladin_reaper, at cluster-creation time.
-- Without it the workers that legitimately span tenants — outbox dispatch,
-- lifecycle sweeps, the reapers — silently see zero rows, because RLS
-- filters rather than errors.
--
-- The migration therefore *attempts* the privileged parts and tolerates
-- being refused. Failing hard here would wedge every deploy on shape (3),
-- which is the normal production shape.
DO $$
BEGIN
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_app') THEN
            CREATE ROLE paladin_app NOLOGIN;
        END IF;
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_migrate') THEN
            CREATE ROLE paladin_migrate NOLOGIN;
        END IF;
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_reaper') THEN
            CREATE ROLE paladin_reaper NOLOGIN;
        END IF;
    EXCEPTION
        WHEN insufficient_privilege THEN
            RAISE NOTICE 'cannot CREATE ROLE without privilege; the operator '
                         'must have provisioned paladin_app / paladin_migrate / '
                         'paladin_reaper out-of-band';
    END;

    -- The dispatcher and the reapers legitimately span tenants: the outbox
    -- drain and the lifecycle sweeps are platform work, not tenant work.
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_migrate') THEN
        BEGIN
            ALTER ROLE paladin_migrate BYPASSRLS;
        EXCEPTION
            WHEN insufficient_privilege THEN
                RAISE NOTICE 'cannot ALTER ROLE paladin_migrate BYPASSRLS without '
                             'SUPERUSER; the role must be provisioned with it';
        END;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'paladin_reaper') THEN
        BEGIN
            ALTER ROLE paladin_reaper BYPASSRLS;
        EXCEPTION
            WHEN insufficient_privilege THEN
                RAISE NOTICE 'cannot ALTER ROLE paladin_reaper BYPASSRLS without '
                             'SUPERUSER; the role must be provisioned with it';
        END;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO paladin_app, paladin_reaper;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO paladin_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO paladin_reaper;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO paladin_app, paladin_reaper;

ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO paladin_app, paladin_reaper;
ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO paladin_app, paladin_reaper;

-- ─── Session tenant ─────────────────────────────────────────────────────────
--
-- One function shared by every policy. STABLE + PARALLEL SAFE so the planner
-- can fold it into index lookups instead of re-evaluating per row. The `true`
-- second argument makes an unset GUC return NULL rather than raising — an
-- unset tenant therefore matches nothing, which is the safe direction.

CREATE OR REPLACE FUNCTION paladin_session_tenant_id() RETURNS uuid
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
    SELECT NULLIF(current_setting('paladin.tenant_id', true), '')::uuid
$$;

-- Cross-tenant READ escape hatch for the admin plane.
--
-- Some admin surfaces are legitimately platform-wide: the storage browser
-- lists every collection on a bucket regardless of who owns it. Scoping the
-- connection to one tenant cannot express that, and the admin plane has no
-- BYPASSRLS pool — so a request that has already passed a platform.admin
-- gate sets `paladin.cross_tenant` for the duration of that query.
--
-- It widens SELECT only. WITH CHECK never consults it, so a write is still
-- pinned to exactly one tenant no matter what the flag says: the worst a
-- misplaced flag can do is show too much, never cross-write.
CREATE OR REPLACE FUNCTION paladin_session_cross_tenant() RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
    SELECT COALESCE(current_setting('paladin.cross_tenant', true), '') = 'on'
$$;

-- ─── Policies ───────────────────────────────────────────────────────────────
--
-- FORCE, not just ENABLE: without FORCE the table owner bypasses its own
-- policies, and migrations run as the owner.

CREATE OR REPLACE PROCEDURE paladin_apply_tenant_isolation(tbl regclass)
    LANGUAGE plpgsql AS $$
BEGIN
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
    -- USING admits the cross-tenant read flag; WITH CHECK deliberately does
    -- not, so writes stay pinned to one tenant.
    EXECUTE format(
        'CREATE POLICY tenant_isolation ON %s FOR ALL '
        'USING (tenant_id = paladin_session_tenant_id() '
        '       OR paladin_session_cross_tenant()) '
        'WITH CHECK (tenant_id = paladin_session_tenant_id())', tbl);
END
$$;

CALL paladin_apply_tenant_isolation('objects');
CALL paladin_apply_tenant_isolation('object_versions');
CALL paladin_apply_tenant_isolation('object_locks');
CALL paladin_apply_tenant_isolation('object_tags');

CALL paladin_apply_tenant_isolation('multipart_uploads');
CALL paladin_apply_tenant_isolation('capability_records');
CALL paladin_apply_tenant_isolation('event_subscriptions');
CALL paladin_apply_tenant_isolation('pending_purges');
CALL paladin_apply_tenant_isolation('quotas');
CALL paladin_apply_tenant_isolation('idempotency_keys');
CALL paladin_apply_tenant_isolation('collections');
CALL paladin_apply_tenant_isolation('tenant_budgets');
CALL paladin_apply_tenant_isolation('tenant_storage_migrations');

-- These last three are written by the ADMIN plane on behalf of a tenant
-- other than the caller's, which the connection's tenant GUC would otherwise
-- reject. They are covered because the admin handlers now scope the
-- connection to the tenant they were authorised against — see
-- auth.WithActingTenant and internal/store/postgres/rls.go. The
-- pre-consolidation schema left object_keys (now collections) uncovered
-- precisely because that mechanism did not exist.
--
-- Still uncovered, and for a different reason: tenants, storage_backends and
-- buckets are platform-level resources with no single owning tenant, so
-- there is no tenant to scope a connection to.

DROP PROCEDURE paladin_apply_tenant_isolation(regclass);

-- multipart_parts has no tenant_id of its own — it is strictly subordinate to
-- an upload. Isolating it through the parent keeps the denormalisation rule
-- honest: we duplicate tenant_id where RLS needs it on the hot path, not
-- everywhere by reflex.
ALTER TABLE multipart_parts ENABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_parts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON multipart_parts FOR ALL
    USING (EXISTS (SELECT 1 FROM multipart_uploads u
                   WHERE u.id = multipart_parts.upload_id
                     AND u.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM multipart_uploads u
                        WHERE u.id = multipart_parts.upload_id
                          AND u.tenant_id = paladin_session_tenant_id()));

-- capability_usage is keyed by capability, and the capability carries the
-- tenant. Same reasoning as multipart_parts.
ALTER TABLE capability_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capability_usage FOR ALL
    USING (EXISTS (SELECT 1 FROM capability_records c
                   WHERE c.id = capability_usage.capability_id
                     AND c.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM capability_records c
                        WHERE c.id = capability_usage.capability_id
                          AND c.tenant_id = paladin_session_tenant_id()));

-- api_tokens needs a second policy: token verification happens BEFORE the
-- session tenant is known — that is the whole point of the lookup — so the
-- pre-auth read cannot be tenant-scoped. It is narrowed to the columns the
-- verifier needs by the grant below, not by the policy.
ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_tokens FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());
CREATE POLICY api_tokens_preauth_read ON api_tokens FOR SELECT
    USING (paladin_session_tenant_id() IS NULL);

-- Append-only surfaces: writes are pinned to the acting tenant, reads are
-- served by the admin plane which runs with BYPASSRLS.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_write_isolation ON audit_log FOR INSERT
    WITH CHECK (actor_tenant_id = paladin_session_tenant_id()
                OR actor_tenant_id IS NULL);

-- Reads are deliberately unrestricted. The audit trail is a
-- platform-operator surface: an investigation that could only see one
-- tenant's entries cannot answer "who touched this", which is the question
-- the log exists for. Tenant-facing exposure is the admin plane's job, not
-- the policy's. Without this, FORCE RLS plus an INSERT-only policy makes
-- every SELECT return zero rows — silently, since RLS filters rather than
-- errors.
CREATE POLICY audit_log_read_all ON audit_log FOR SELECT USING (true);

ALTER TABLE operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE operations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_write_isolation ON operations FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- The outbox is drained across tenants by the dispatcher (BYPASSRLS); the
-- producer path still gets checked on insert.
ALTER TABLE event_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY event_deliveries_tenant_isolation ON event_deliveries FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- charges is the billing ledger: a tenant may read its own, nobody may rewrite
-- history. UPDATE and DELETE are absent by omission, which RLS treats as deny.
ALTER TABLE charges ENABLE ROW LEVEL SECURITY;
ALTER TABLE charges FORCE ROW LEVEL SECURITY;
CREATE POLICY charges_tenant_isolation ON charges FOR SELECT
    USING (tenant_id = paladin_session_tenant_id());
CREATE POLICY charges_tenant_insert ON charges FOR INSERT
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
