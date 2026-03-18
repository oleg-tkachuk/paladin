-- +goose Up
-- +goose StatementBegin
ALTER TABLE audit_logs
ADD COLUMN trace_id text;

-- Create an index to quickly filter audit logs by trace ID
CREATE INDEX idx_audit_logs_trace_id ON audit_logs (tenant_id, trace_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_audit_logs_trace_id;
ALTER TABLE audit_logs
DROP COLUMN trace_id;
-- +goose StatementEnd
