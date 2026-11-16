-- +goose Up
-- +goose StatementBegin

-- A slug a purged tenant held. Cedar names a tenant by its slug, and so do
-- policies, tokens and every `tenants/<slug>` a client keeps, so a slug handed
-- to a new tenant would make all of them name it instead. CreateTenant
-- refuses a slug recorded here, and one a trashed tenant still holds, unless
-- admin.tenants.reserve_slugs is off. The purge records it in the same
-- transaction that deletes the tenant, whatever that setting says.
--
-- No tenant_id column and no policy: the row outlives its tenant, and the
-- create path reads it before any tenant scope exists.
CREATE TABLE retired_tenant_slugs (
    slug             text        PRIMARY KEY,
    former_tenant_id uuid        NOT NULL,
    retired_at       timestamptz NOT NULL DEFAULT now()
);

-- RenameTenantSlug is gone (109): the slug never changes, and the session
-- setting that let the rename past this trigger has no caller left.
CREATE OR REPLACE FUNCTION tenants_block_immutable_columns() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id THEN
        RAISE EXCEPTION 'tenants.id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        RAISE EXCEPTION 'tenants.slug is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION tenants_block_immutable_columns() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    allow_rename text := current_setting('paladin.allow_slug_rename', true);
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id THEN
        RAISE EXCEPTION 'tenants.id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug
       AND allow_rename IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION 'tenants.slug is immutable; use the RenameTenantSlug RPC'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

DROP TABLE retired_tenant_slugs;

-- +goose StatementEnd
