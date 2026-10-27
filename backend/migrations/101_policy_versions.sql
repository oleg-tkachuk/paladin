-- +goose Up

-- A Cedar policy is stored in place on its storage backend, bucket,
-- collection or tenant, so a write used to leave no trace of the text it
-- replaced. policy_versions keeps every text each layer has held, with who
-- wrote it and when (ADR-0040).
--
-- Triggers write it, not the application: every path that sets a policy — an
-- RPC, a tenant's default policy at creation, an operator's psql — reaches
-- the same row, and none can forget to record. Who wrote it comes from
-- paladin.actor, which the pool sets on every acquire as it does
-- paladin.tenant_id; NULL where nothing set it (a migration, psql).

CREATE TYPE policy_layer AS ENUM ('storage_backend', 'bucket', 'collection', 'tenant');

CREATE TABLE policy_versions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    layer        policy_layer NOT NULL,
    -- The id of the row holding the layer: a storage backend, bucket,
    -- collection or tenant.
    owner_id     uuid         NOT NULL,
    -- The tenant a collection or tenant layer belongs to, which RLS keys on;
    -- NULL for the platform-level backend and bucket layers.
    tenant_id    uuid,
    version      integer      NOT NULL,
    cedar_policy text         NOT NULL,
    -- The subject of the principal whose request wrote it; NULL when no
    -- request did.
    written_by   text,
    written_at   timestamptz  NOT NULL DEFAULT now(),
    UNIQUE (layer, owner_id, version)
);

-- Readable where its owner is: a platform layer by anyone the RPC lets in, a
-- tenant's under the same isolation as its collections. No INSERT, UPDATE or
-- DELETE policy: only the SECURITY DEFINER triggers below write, so history
-- cannot be forged or rewritten from the application.
ALTER TABLE policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE policy_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY policy_versions_read ON policy_versions FOR SELECT
    USING (tenant_id IS NULL
           OR tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant());

-- +goose StatementBegin
-- record_policy_version appends NEW's policy as the layer's next version.
-- TG_ARGV: the layer, the column holding the policy, and the column naming
-- its tenant ('' for none).
CREATE OR REPLACE FUNCTION record_policy_version() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_row    jsonb := to_jsonb(NEW);
    v_layer  public.policy_layer := TG_ARGV[0]::public.policy_layer;
    v_tenant uuid := CASE WHEN TG_ARGV[2] = '' THEN NULL ELSE (v_row ->> TG_ARGV[2])::uuid END;
BEGIN
    INSERT INTO public.policy_versions (layer, owner_id, tenant_id, version, cedar_policy, written_by)
    SELECT v_layer, NEW.id, v_tenant,
           COALESCE(MAX(pv.version), 0) + 1,
           COALESCE(v_row ->> TG_ARGV[1], ''),
           NULLIF(current_setting('paladin.actor', true), '')
      FROM public.policy_versions pv
     WHERE pv.layer = v_layer AND pv.owner_id = NEW.id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- forget_policy_versions drops a deleted row's history with it.
CREATE OR REPLACE FUNCTION forget_policy_versions() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
BEGIN
    DELETE FROM public.policy_versions
     WHERE layer = TG_ARGV[0]::public.policy_layer AND owner_id = OLD.id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION record_policy_version() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION forget_policy_versions() FROM PUBLIC;

-- A row created with a policy records it as version 1; an update records a
-- text that differs from the one it replaces, a cleared policy included.
CREATE TRIGGER storage_backends_policy_insert AFTER INSERT ON storage_backends
    FOR EACH ROW WHEN (COALESCE(NEW.cedar_policy, '') <> '')
    EXECUTE FUNCTION record_policy_version('storage_backend', 'cedar_policy', '');
CREATE TRIGGER storage_backends_policy_update AFTER UPDATE OF cedar_policy ON storage_backends
    FOR EACH ROW WHEN (OLD.cedar_policy IS DISTINCT FROM NEW.cedar_policy)
    EXECUTE FUNCTION record_policy_version('storage_backend', 'cedar_policy', '');
CREATE TRIGGER storage_backends_policy_delete AFTER DELETE ON storage_backends
    FOR EACH ROW EXECUTE FUNCTION forget_policy_versions('storage_backend');

CREATE TRIGGER buckets_policy_insert AFTER INSERT ON buckets
    FOR EACH ROW WHEN (COALESCE(NEW.cedar_policy, '') <> '')
    EXECUTE FUNCTION record_policy_version('bucket', 'cedar_policy', '');
CREATE TRIGGER buckets_policy_update AFTER UPDATE OF cedar_policy ON buckets
    FOR EACH ROW WHEN (OLD.cedar_policy IS DISTINCT FROM NEW.cedar_policy)
    EXECUTE FUNCTION record_policy_version('bucket', 'cedar_policy', '');
CREATE TRIGGER buckets_policy_delete AFTER DELETE ON buckets
    FOR EACH ROW EXECUTE FUNCTION forget_policy_versions('bucket');

CREATE TRIGGER collections_policy_insert AFTER INSERT ON collections
    FOR EACH ROW WHEN (COALESCE(NEW.cedar_policy, '') <> '')
    EXECUTE FUNCTION record_policy_version('collection', 'cedar_policy', 'tenant_id');
CREATE TRIGGER collections_policy_update AFTER UPDATE OF cedar_policy ON collections
    FOR EACH ROW WHEN (OLD.cedar_policy IS DISTINCT FROM NEW.cedar_policy)
    EXECUTE FUNCTION record_policy_version('collection', 'cedar_policy', 'tenant_id');
CREATE TRIGGER collections_policy_delete AFTER DELETE ON collections
    FOR EACH ROW EXECUTE FUNCTION forget_policy_versions('collection');

CREATE TRIGGER tenants_policy_insert AFTER INSERT ON tenants
    FOR EACH ROW WHEN (COALESCE(NEW.inherited_cedar_policy, '') <> '')
    EXECUTE FUNCTION record_policy_version('tenant', 'inherited_cedar_policy', 'id');
CREATE TRIGGER tenants_policy_update AFTER UPDATE OF inherited_cedar_policy ON tenants
    FOR EACH ROW WHEN (OLD.inherited_cedar_policy IS DISTINCT FROM NEW.inherited_cedar_policy)
    EXECUTE FUNCTION record_policy_version('tenant', 'inherited_cedar_policy', 'id');
CREATE TRIGGER tenants_policy_delete AFTER DELETE ON tenants
    FOR EACH ROW EXECUTE FUNCTION forget_policy_versions('tenant');

-- Every layer that holds a policy now starts its history with it.
INSERT INTO policy_versions (layer, owner_id, tenant_id, version, cedar_policy)
SELECT 'storage_backend'::policy_layer, id, NULL::uuid, 1, cedar_policy FROM storage_backends WHERE COALESCE(cedar_policy, '') <> ''
UNION ALL
SELECT 'bucket'::policy_layer, id, NULL::uuid, 1, cedar_policy FROM buckets WHERE COALESCE(cedar_policy, '') <> ''
UNION ALL
SELECT 'collection', id, tenant_id, 1, cedar_policy FROM collections WHERE COALESCE(cedar_policy, '') <> ''
UNION ALL
SELECT 'tenant', id, id, 1, inherited_cedar_policy FROM tenants WHERE COALESCE(inherited_cedar_policy, '') <> '';

-- +goose Down
DROP TRIGGER IF EXISTS tenants_policy_delete ON tenants;
DROP TRIGGER IF EXISTS tenants_policy_update ON tenants;
DROP TRIGGER IF EXISTS tenants_policy_insert ON tenants;
DROP TRIGGER IF EXISTS collections_policy_delete ON collections;
DROP TRIGGER IF EXISTS collections_policy_update ON collections;
DROP TRIGGER IF EXISTS collections_policy_insert ON collections;
DROP TRIGGER IF EXISTS buckets_policy_delete ON buckets;
DROP TRIGGER IF EXISTS buckets_policy_update ON buckets;
DROP TRIGGER IF EXISTS buckets_policy_insert ON buckets;
DROP TRIGGER IF EXISTS storage_backends_policy_delete ON storage_backends;
DROP TRIGGER IF EXISTS storage_backends_policy_update ON storage_backends;
DROP TRIGGER IF EXISTS storage_backends_policy_insert ON storage_backends;
DROP FUNCTION IF EXISTS forget_policy_versions();
DROP FUNCTION IF EXISTS record_policy_version();
DROP TABLE IF EXISTS policy_versions;
DROP TYPE IF EXISTS policy_layer;
