-- +goose Up

-- The resource an audited action changed, as its own Get read it before and
-- after, redacted (state_before_json / state_after_json on AuditLogEntry).
-- Catalog changes only: nullable, no default.
ALTER TABLE audit_log ADD COLUMN state_before_json bytea;
ALTER TABLE audit_log ADD COLUMN state_after_json bytea;

-- +goose Down
ALTER TABLE audit_log DROP COLUMN state_after_json;
ALTER TABLE audit_log DROP COLUMN state_before_json;
