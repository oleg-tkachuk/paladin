-- +goose Up
-- +goose StatementBegin

-- Store.Insert is given the whole principal that asked for a capability, and
-- only its subject was kept (created_by), so Store.GetRecord could not give
-- back what Insert received. issued_by holds the principal as JSON, beside
-- created_by, which stays for what already reads it. Rows written before
-- this have none, and read back with the subject alone.
--
-- A nullable column with no default: a metadata-only change (CONVENTIONS.md).
ALTER TABLE capability_records ADD COLUMN issued_by jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE capability_records DROP COLUMN issued_by;
-- +goose StatementEnd
