-- +goose Up
-- +goose StatementBegin

CREATE TABLE tenants (
    id           UUID         NOT NULL DEFAULT gen_random_uuid(),
    tenant_id    TEXT         NOT NULL,
    display_name TEXT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT pk_tenants PRIMARY KEY (id),
    CONSTRAINT uq_tenants_tenant_id UNIQUE (tenant_id)
);

CREATE INDEX idx_tenants_tenant_id ON tenants (tenant_id);

CREATE TRIGGER set_tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Backfill any tenant IDs already present in the system.
-- Uses UNION to collect from both objects and object_categories to be exhaustive.
INSERT INTO tenants (tenant_id)
SELECT DISTINCT tenant_id
FROM (
    SELECT tenant_id FROM objects
    UNION
    SELECT tenant_id FROM object_categories
) existing
ON CONFLICT (tenant_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tenants;
-- +goose StatementEnd
