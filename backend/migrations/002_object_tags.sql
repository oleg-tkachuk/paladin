-- +goose Up
-- +goose StatementBegin

-- Object tags are tenant-scoped taxonomy entries. Objects are NOT linked to
-- object tags in this migration — classification happens via object labels
-- referencing object-tag slugs, so removing an object tag is safe. If direct
-- FK linkage is needed later, add an object_object_tags join table.
CREATE TABLE object_tags (
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    slug             TEXT NOT NULL,
    display_name     TEXT,
    description      TEXT NOT NULL DEFAULT '',
    labels           JSONB NOT NULL DEFAULT '{}'::jsonb,
    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, slug),
    CONSTRAINT object_tag_slug_format CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')
);

CREATE INDEX idx_object_tags_labels_gin ON object_tags USING GIN (labels);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_tags;
-- +goose StatementEnd
