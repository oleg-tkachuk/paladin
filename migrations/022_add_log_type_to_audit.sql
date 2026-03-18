-- +goose Up
-- +goose StatementBegin
ALTER TABLE audit_logs ADD COLUMN log_type text NOT NULL DEFAULT 'audit';
CREATE INDEX idx_audit_logs_log_type ON audit_logs (tenant_id, log_type);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_audit_logs_log_type;
ALTER TABLE audit_logs DROP COLUMN log_type;
-- +goose StatementEnd
