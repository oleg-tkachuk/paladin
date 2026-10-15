-- +goose Up
-- +goose StatementBegin

-- Every bucket has a tenant owner, and sharing is granted (ADR-0032).
--
-- owner_tenant_id was nullable, and NULL meant "shared": the binding trigger
-- let any tenant bind a collection to such a bucket. Sharing is now a row in
-- bucket_grants that the owner made, and a bucket with no owner is gone.
--
-- bucket_grants is platform configuration, like buckets and bucket_quotas
-- (ADR-0025), and is deliberately left without row-level security: the
-- binding trigger reads it under the inserting session, and a grant hidden
-- from that session would read as no grant and refuse a binding it allows.
-- Writes are gated by the bucket owner's ManageBucket in the handler.
CREATE TABLE bucket_grants (
    bucket_id  uuid NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    granted_by text NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_id, tenant_id)
);
-- "Which buckets may this tenant bind?" — listings and the default binding.
CREATE INDEX bucket_grants_tenant_idx ON bucket_grants (tenant_id);

-- The ownerless rows. A tenant uses a bucket when it has a collection bound
-- to it or its default binding names it.
--   - used by one tenant: that tenant owns it;
--   - used by several: the platform tenant owns it, and each gets a grant;
--   - used by none: the platform tenant owns it.
-- The platform tenant is found by the bootstrap's default slug: a migration
-- cannot read the config. A deployment that renamed it fails here, by name,
-- rather than guess (docs/upgrading.md).
DO $$
DECLARE
    platform uuid;
BEGIN
    UPDATE buckets b
       SET owner_tenant_id = u.tenant_id
      FROM (SELECT bucket_id, min(tenant_id::text)::uuid AS tenant_id
              FROM (SELECT bucket_id, tenant_id FROM collections
                    UNION
                    SELECT bucket_id, tenant_id FROM tenant_default_bindings) used
             GROUP BY bucket_id
            HAVING count(DISTINCT tenant_id) = 1) u
     WHERE b.id = u.bucket_id AND b.owner_tenant_id IS NULL;

    IF NOT EXISTS (SELECT 1 FROM buckets WHERE owner_tenant_id IS NULL) THEN
        RETURN;
    END IF;
    SELECT id INTO platform FROM tenants WHERE slug = 'platform';
    IF platform IS NULL THEN
        RAISE EXCEPTION 'buckets with no owner remain and no tenant has the slug "platform" to own them; '
            'set owner_tenant_id on them, or rename the platform tenant to "platform", and migrate again';
    END IF;

    INSERT INTO bucket_grants (bucket_id, tenant_id, granted_by)
    SELECT DISTINCT used.bucket_id, used.tenant_id, 'migration 080'
      FROM (SELECT bucket_id, tenant_id FROM collections
            UNION
            SELECT bucket_id, tenant_id FROM tenant_default_bindings) used
      JOIN buckets b ON b.id = used.bucket_id
     WHERE b.owner_tenant_id IS NULL AND used.tenant_id <> platform;

    UPDATE buckets SET owner_tenant_id = platform WHERE owner_tenant_id IS NULL;
END
$$;

ALTER TABLE buckets ALTER COLUMN owner_tenant_id SET NOT NULL;

-- The owner, or a tenant holding a grant, may bind a collection to a bucket;
-- nobody else. The rule is named as the error's constraint, so the
-- application maps it without reading the message (as 049 does).
CREATE OR REPLACE FUNCTION collections_enforce_bucket_tenancy() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    owner uuid;
BEGIN
    SELECT owner_tenant_id INTO owner FROM buckets WHERE id = NEW.bucket_id;
    IF NOT FOUND THEN
        -- No such bucket: the foreign key says so, in its own words.
        RETURN NEW;
    END IF;
    IF owner = NEW.tenant_id OR EXISTS (
        SELECT 1 FROM bucket_grants g
         WHERE g.bucket_id = NEW.bucket_id AND g.tenant_id = NEW.tenant_id) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'collection for tenant % may not bind to bucket % owned by tenant %',
        NEW.tenant_id, NEW.bucket_id, owner
        USING ERRCODE = 'check_violation', CONSTRAINT = 'collections_bucket_tenancy';
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
ALTER TABLE buckets ALTER COLUMN owner_tenant_id DROP NOT NULL;
DROP TABLE bucket_grants;
-- +goose StatementEnd
