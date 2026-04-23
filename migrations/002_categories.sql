-- +goose Up
-- +goose StatementBegin

-- Categories are tenant-scoped taxonomy entries. Objects are NOT linked to
-- categories in this migration — categorization happens via object labels
-- referencing category slugs, so removing a category is safe. If direct FK
-- linkage is needed later, add an object_categories join table.
CREATE TABLE categories (
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    slug             TEXT NOT NULL,
    display_name     TEXT,
    description      TEXT NOT NULL DEFAULT '',
    labels           JSONB NOT NULL DEFAULT '{}'::jsonb,
    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, slug),
    CONSTRAINT category_slug_format CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')
);

CREATE INDEX idx_categories_labels_gin ON categories USING GIN (labels);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS categories;
-- +goose StatementEnd
