-- +goose Up

-- detail and outcome date from the initial schema and no writer has ever set
-- either, so every row holds their defaults. Catalog changes only.
ALTER TABLE audit_log DROP COLUMN detail;
ALTER TABLE audit_log DROP COLUMN outcome;

-- +goose Down
ALTER TABLE audit_log ADD COLUMN outcome text NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN detail jsonb NOT NULL DEFAULT '{}'::jsonb;
