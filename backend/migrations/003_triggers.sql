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

-- ─── Dedicated-bucket tenancy ───────────────────────────────────────────────
--
-- A dedicated bucket belongs to exactly one tenant (buckets.owner_tenant_id).
-- Binding a collection to a bucket someone else owns would hand that tenant a
-- write path into the owner's storage — RLS cannot catch it, because the row
-- being inserted carries the ATTACKER's tenant_id and so passes the policy
-- cleanly. The check has to compare the two tenants, which is what this does.
--
-- Shared buckets (owner_tenant_id IS NULL) are bindable by anyone: that is
-- what makes them shared.
CREATE OR REPLACE FUNCTION collections_enforce_bucket_tenancy() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    owner uuid;
BEGIN
    SELECT owner_tenant_id INTO owner FROM buckets WHERE id = NEW.bucket_id;
    IF owner IS NOT NULL AND owner <> NEW.tenant_id THEN
        RAISE EXCEPTION
            'collection for tenant % may not bind to bucket % owned by tenant %',
            NEW.tenant_id, NEW.bucket_id, owner
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER collections_enforce_bucket_tenancy
    BEFORE INSERT OR UPDATE OF bucket_id, tenant_id ON collections
    FOR EACH ROW EXECUTE FUNCTION collections_enforce_bucket_tenancy();

-- ─── Policy invalidation ────────────────────────────────────────────────────
--
-- The Cedar engine caches policies per scope and listens on channel
-- `policy_changed` (internal/policy/cedar/store.go). Both the channel name and
-- the payload shape are a contract with that listener:
--
--     "<tenant_uuid>"              → invalidate the tenant's inherited policy
--     "<tenant_uuid>:<collection>" → invalidate one collection
--
-- The guard compares the policy TEXT, not the hash: the hash is computed by
-- the application after the write, so a hash-based guard stays silent on the
-- statement that actually changed the policy.
CREATE OR REPLACE FUNCTION paladin_notify_collection_policy_changed() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    -- DELETE has no NEW: the row is going away, so the cached policy for it
    -- must go too, and OLD carries the scope to name.
    IF TG_OP = 'DELETE' THEN
        IF OLD.cedar_policy <> '' THEN
            PERFORM pg_notify('policy_changed',
                              OLD.tenant_id::text || ':' || OLD.name);
        END IF;
        RETURN OLD;
    END IF;
    -- INSERT has no OLD, and TG_OP='INSERT' means the scope did not exist a
    -- moment ago — the engine caches empty Fetch results, so creation must
    -- invalidate just as an edit does.
    IF TG_OP = 'INSERT' THEN
        IF NEW.cedar_policy <> '' THEN
            PERFORM pg_notify('policy_changed',
                              NEW.tenant_id::text || ':' || NEW.name);
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.cedar_policy IS DISTINCT FROM OLD.cedar_policy THEN
        PERFORM pg_notify('policy_changed',
                          NEW.tenant_id::text || ':' || NEW.name);
    END IF;
    RETURN NEW;
END
$$;

-- tenants spells the same thing differently: its policy is inherited by
-- children, so the column is inherited_cedar_policy. A shared trigger function
-- referencing NEW.cedar_policy raises "record new has no field" on every
-- tenant UPDATE — a plpgsql runtime error, not a compile-time one, so it only
-- appears when a tenant is actually written. The payload is a bare uuid: the
-- tenant IS the scope, so there is no collection segment to append.
CREATE OR REPLACE FUNCTION paladin_notify_tenant_policy_changed() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    IF NEW.inherited_cedar_policy IS DISTINCT FROM OLD.inherited_cedar_policy THEN
        PERFORM pg_notify('policy_changed', NEW.id::text);
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER tenants_notify_policy_changed
    AFTER UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_tenant_policy_changed();
CREATE TRIGGER collections_notify_policy_changed
    AFTER INSERT OR UPDATE OR DELETE ON collections
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_collection_policy_changed();

-- buckets and storage_backends carry policies too, but neither is
-- tenant-scoped, so neither can name a scope in the payload format above.
-- Their invalidation is the engine's ResyncAll path, driven by the admin
-- plane on write rather than by a trigger — a bucket policy change affects
-- every tenant bound to it, which is a flush, not a targeted eviction.

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
