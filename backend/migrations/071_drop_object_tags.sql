-- +goose Up
-- +goose StatementBegin

-- object_tags held tag definitions per tenant. Nothing reads or writes it: the
-- handler that would have was never constructed by any RPC and was removed,
-- and an object's own tags live in objects.tags. Empty in every environment
-- when this was written; refuse rather than drop rows someone wrote since.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM object_tags) THEN
        RAISE EXCEPTION 'object_tags holds rows; nothing reads them, but decide before dropping them';
    END IF;
END
$$;

DROP TABLE object_tags;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The table as 001_initial_schema.sql, 002_roles_and_rls.sql and
-- 003_triggers.sql made it, empty. 002's isolation procedure is gone by now,
-- so its body is spelled out, as 011 does.
CREATE TABLE object_tags (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slug             text NOT NULL,
    display_name     text,
    description      text NOT NULL DEFAULT '',
    labels           jsonb NOT NULL DEFAULT '{}'::jsonb,
    resource_version bigint NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug),
    CONSTRAINT object_tag_slug_format
        CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')
);
ALTER TABLE object_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON object_tags FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());
CREATE TRIGGER object_tags_bump_resource_version BEFORE UPDATE ON object_tags
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- +goose StatementEnd
