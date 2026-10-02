-- +goose Up
-- +goose StatementBegin

-- Taint signals on objects: the content was flagged as carrying a prompt
-- injection, personal data or a secret. A capability without
-- AllowTaintedRead is refused a read of a tainted object; until now the
-- caveat existed and restricted nothing, because no object could be flagged.
--
-- Set and cleared through SetObjectTaint, by a person or, later, a scanner.
-- An empty array is a clean object. The signal names are validated by the
-- application, which owns the closed set.
--
-- A constant default, so the column is added as a metadata change without
-- rewriting the table (CONVENTIONS.md).
ALTER TABLE objects ADD COLUMN taint text[] NOT NULL DEFAULT '{}';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE objects DROP COLUMN IF EXISTS taint;
-- +goose StatementEnd
