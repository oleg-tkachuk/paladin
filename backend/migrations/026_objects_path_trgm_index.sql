-- +goose NO TRANSACTION
-- +goose Up

-- Answers `key.contains(q)` and `key.startsWith(q)` pushed down as LIKE.
-- Patterns shorter than three characters fall back to the keyset index, which
-- is the same plan as before this index existed. CONCURRENTLY and on its own,
-- per CONVENTIONS.md: every upload writes this table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_path_trgm
    ON objects USING gin (path gin_trgm_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_path_trgm;
