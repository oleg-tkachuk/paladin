-- +goose Up

-- A writer's subject names a user only within a tenant, and a platform admin
-- who edits a tenant's policy lives in another one, so the console could not
-- name the writer of most versions. Record the writer's own tenant beside
-- the subject, as the audit log records an actor's (ADR-0040). The pool sets
-- paladin.actor_tenant with paladin.actor; NULL where nothing set it.
ALTER TABLE policy_versions ADD COLUMN written_by_tenant_id uuid;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_policy_version() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_row    jsonb := to_jsonb(NEW);
    v_layer  public.policy_layer := TG_ARGV[0]::public.policy_layer;
    v_tenant uuid := CASE WHEN TG_ARGV[2] = '' THEN NULL ELSE (v_row ->> TG_ARGV[2])::uuid END;
BEGIN
    INSERT INTO public.policy_versions
        (layer, owner_id, tenant_id, version, cedar_policy, written_by, written_by_tenant_id)
    SELECT v_layer, NEW.id, v_tenant,
           COALESCE(MAX(pv.version), 0) + 1,
           COALESCE(v_row ->> TG_ARGV[1], ''),
           NULLIF(current_setting('paladin.actor', true), ''),
           NULLIF(current_setting('paladin.actor_tenant', true), '')::uuid
      FROM public.policy_versions pv
     WHERE pv.layer = v_layer AND pv.owner_id = NEW.id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
ALTER TABLE policy_versions DROP COLUMN IF EXISTS written_by_tenant_id;
