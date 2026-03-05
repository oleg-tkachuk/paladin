-- +goose Up
-- +goose StatementBegin

ALTER TABLE tenants
    ADD COLUMN labels JSONB    NOT NULL DEFAULT '{}',
    ADD COLUMN tags   TEXT[]   NOT NULL DEFAULT '{}';

-- GIN index for label containment queries: labels @> $filter
CREATE INDEX idx_tenants_labels ON tenants USING GIN (labels);

-- GIN index for tag overlap queries: tags && ARRAY[...]
CREATE INDEX idx_tenants_tags ON tenants USING GIN (tags);

COMMENT ON COLUMN tenants.labels IS 'Arbitrary key-value metadata. Keys are strings, values are strings.';
COMMENT ON COLUMN tenants.tags IS 'Unordered set of string tags for categorical filtering.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tenants_labels;
DROP INDEX IF EXISTS idx_tenants_tags;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS labels,
    DROP COLUMN IF EXISTS tags;
-- +goose StatementEnd
