-- +goose NO TRANSACTION
-- +goose Up

-- Keys in byte order within a collection, the order every listing uses
-- (085). The unique (collection_id, path) index is in the database's
-- collation, so it cannot serve a walk over path COLLATE "C"; this one lets a
-- folder listing (106) jump from one folder to the next instead of reading
-- every key under the prefix. CONCURRENTLY and on its own, per
-- CONVENTIONS.md: every upload writes this table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_collection_path_c
    ON objects (collection_id, path COLLATE "C");

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_collection_path_c;
