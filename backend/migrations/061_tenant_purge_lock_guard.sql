-- +goose Up
-- +goose StatementBegin

-- A tenant that holds a locked object cannot be purged, and now that is a
-- check of its own rather than a consequence of the order things are refused
-- in.
--
-- Until this file, PurgeTenant was stopped by collections_tenant_id_fkey: an
-- object needs a collection, a collection RESTRICTs its tenant, so a tenant
-- with any object could not go. That is true, but it protects a locked object
-- only as a side effect of protecting every object, and the refusal said
-- "collections" — nothing about a legal hold. The other guard,
-- object_locks_enforce_retention, fires on the object_locks rows the tenant
-- delete CASCADEs to; it holds, but only if the cascade reaches it before a
-- RESTRICT elsewhere aborts the statement, and its check_violation reached the
-- caller as an unmapped Internal error.
--
-- This trigger runs BEFORE the row goes, so before any foreign-key action,
-- and refuses while anything the tenant owns is under a legal hold or an
-- unexpired retention window. The rules are those of
-- enforce_object_lock_retention: a hold and COMPLIANCE are absolute,
-- GOVERNANCE yields only to paladin.bypass_governance_retention, which the
-- purge path never sets.
--
-- Two ways a lock belongs to the tenant, both checked: the lock row names it,
-- or the lock covers a version of one of its objects. They agree today; the
-- check does not depend on that.
--
-- objects, object_versions and object_locks are under FORCE ROW LEVEL
-- SECURITY. A session scoped to another tenant, or to none, would see no rows
-- and pass the check, so the function runs as its owner, the migrate role,
-- which holds BYPASSRLS (as search_object_ids in 029 does). It only reads,
-- and the search_path is pinned with every table schema-qualified.
--
-- Not a SET paladin.cross_tenant clause: setting a custom parameter in a
-- function definition needs superuser in Postgres 15 and later, and the
-- migrate role is the database owner and nothing more.
CREATE OR REPLACE FUNCTION enforce_tenant_purge_lock_guard() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    bypass boolean := current_setting('paladin.bypass_governance_retention', true) IS NOT DISTINCT FROM 'on';
    held   uuid;
BEGIN
    SELECT l.version_id INTO held
      FROM public.object_locks l
     WHERE (l.tenant_id = OLD.id
            OR l.version_id IN (SELECT v.id
                                  FROM public.objects o
                                  JOIN public.object_versions v ON v.object_id = o.id
                                 WHERE o.tenant_id = OLD.id))
       AND (l.legal_hold
            OR (l.retain_until > now()
                AND (l.mode = 'COMPLIANCE' OR NOT bypass)))
     LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION
            'tenant % holds object version % under legal hold or retention',
            OLD.id, held
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END
$$;

-- A SECURITY DEFINER function is executable by PUBLIC unless revoked. The
-- trigger needs no grant: Postgres checks EXECUTE when the trigger is created,
-- not when it fires.
REVOKE ALL ON FUNCTION enforce_tenant_purge_lock_guard() FROM PUBLIC;

DROP TRIGGER IF EXISTS tenants_purge_lock_guard ON tenants;
CREATE TRIGGER tenants_purge_lock_guard
    BEFORE DELETE ON tenants
    FOR EACH ROW EXECUTE FUNCTION enforce_tenant_purge_lock_guard();

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS tenants_purge_lock_guard ON tenants;
DROP FUNCTION IF EXISTS enforce_tenant_purge_lock_guard();
-- +goose StatementEnd
