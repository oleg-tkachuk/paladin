-- +goose Up

-- Trigram matching for object-key search. The console's search box sends
-- `key.contains(q)`, which the objects query pushes down as
-- `path LIKE '%q%'`; a B-tree cannot answer a leading wildcard, so without
-- this every keystroke scanned the whole collection. pg_trgm ships with
-- Postgres and is a trusted extension (13+), so the migrate role creates it
-- without superuser. The index that uses it is 026, on its own (CONVENTIONS.md).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
-- The extension is left in place: 026's Down drops the only index that uses
-- it, and dropping an extension another schema object might have come to
-- depend on is not a rollback, it is an outage.
SELECT 1;
