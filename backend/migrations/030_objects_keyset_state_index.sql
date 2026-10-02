-- +goose NO TRANSACTION
-- +goose Up

-- idx_objects_keyset with the object's state carried in the leaf.
--
-- A page of one collection is `tenant_id, collection_id … ORDER BY id LIMIT n`,
-- and the state filter — the console's status dropdown, the trash page's
-- `state == 'DELETED'` — was a heap fetch per index entry to read one column.
-- search_object_ids (029) returns ids only, so with state in the index the
-- page is an index-only scan: a status-filtered page of 500 from a 5000-object
-- collection went from 13–16 ms to 5 ms. Same key columns as the index it
-- replaces (031), so every query that used that one can use this.
--
-- CONCURRENTLY and on its own, per CONVENTIONS.md. The old index is dropped
-- in 031, after this one is built, so the keyset is never unindexed.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_keyset_state
    ON objects (tenant_id, collection_id, id) INCLUDE (state);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_keyset_state;
