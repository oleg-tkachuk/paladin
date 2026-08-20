-- +goose Up
-- +goose StatementBegin

-- Triggers.
--
-- Two of the triggers this schema used to carry are gone, replaced by
-- declarative constraints in 001:
--
--   enforce_collection_bucket_tenancy → composite FK objects(tenant_id,
--       collection_id) → collections(tenant_id, id)
--   enforce_user_settings_tenant      → composite FK user_settings(tenant_id,
--       user_id) → users(tenant_id, id)
--
-- A constraint the planner understands beats a trigger that fires per row: it
-- cannot be skipped by a bulk path, and it documents itself in \d.

-- ─── Optimistic concurrency ─────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION bump_resource_version() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    -- Only auto-bump when the caller did not set a version itself: a
    -- compare-and-swap update supplies the expected value and must keep it.
    IF NEW.resource_version IS NULL
       OR NEW.resource_version = OLD.resource_version THEN
        NEW.resource_version := OLD.resource_version + 1;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END
$$;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'tenants', 'users', 'user_settings', 'storage_backends', 'buckets',
        'collections', 'objects', 'object_tags', 'quotas', 'event_subscriptions'
    ] LOOP
        EXECUTE format(
            'CREATE TRIGGER %I_bump_resource_version BEFORE UPDATE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION bump_resource_version()', t, t);
    END LOOP;
END
$$;

-- ─── Immutable tenant identity ──────────────────────────────────────────────

CREATE OR REPLACE FUNCTION tenants_block_immutable_columns() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    allow_rename text := current_setting('paladin.allow_slug_rename', true);
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id THEN
        RAISE EXCEPTION 'tenants.id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    -- The slug is a resource name clients hold. Renaming it is a deliberate
    -- operation that records history, not an UPDATE anyone can issue.
    IF NEW.slug IS DISTINCT FROM OLD.slug
       AND allow_rename IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION 'tenants.slug is immutable; use the RenameTenantSlug RPC'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER tenants_block_immutable_columns
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION tenants_block_immutable_columns();

-- ─── Object Lock retention ──────────────────────────────────────────────────
--
-- Object Lock now lives in its own table (ADR-0013), so the guard moves with
-- it: deleting a version means deleting its lock row, and the lock is what
-- forbids the delete. COMPLIANCE cannot be overridden by anyone, including the
-- platform; GOVERNANCE yields to an explicit bypass GUC.

CREATE OR REPLACE FUNCTION enforce_object_lock_retention() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    bypass text := current_setting('paladin.bypass_governance_retention', true);
BEGIN
    IF OLD.legal_hold THEN
        RAISE EXCEPTION 'object version % is under legal hold', OLD.version_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.retain_until IS NOT NULL AND OLD.retain_until > now() THEN
        IF OLD.mode = 'COMPLIANCE' THEN
            RAISE EXCEPTION
                'object version % is retained under COMPLIANCE until %',
                OLD.version_id, OLD.retain_until
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.mode = 'GOVERNANCE' AND bypass IS DISTINCT FROM 'on' THEN
            RAISE EXCEPTION
                'object version % is retained under GOVERNANCE until %',
                OLD.version_id, OLD.retain_until
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN OLD;
END
$$;

CREATE TRIGGER object_locks_enforce_retention
    BEFORE DELETE ON object_locks
    FOR EACH ROW EXECUTE FUNCTION enforce_object_lock_retention();

-- ─── LISTEN/NOTIFY fan-out ──────────────────────────────────────────────────
--
-- Cheap cache invalidation for policy material the planes hold in memory.
-- NOTIFY is best-effort by design: a missed notification costs a stale cache
-- until its TTL, never correctness, because every authorisation decision
-- re-reads the policy hash.

CREATE OR REPLACE FUNCTION paladin_notify_policy_changed() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    IF NEW.cedar_policy_hash IS DISTINCT FROM OLD.cedar_policy_hash THEN
        PERFORM pg_notify('paladin_policy_changed',
                          TG_TABLE_NAME || ':' || NEW.id::text);
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER tenants_notify_policy_changed
    AFTER UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_policy_changed();
CREATE TRIGGER collections_notify_policy_changed
    AFTER UPDATE ON collections
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_policy_changed();
CREATE TRIGGER buckets_notify_policy_changed
    AFTER UPDATE ON buckets
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_policy_changed();
CREATE TRIGGER storage_backends_notify_policy_changed
    AFTER UPDATE ON storage_backends
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_policy_changed();

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
