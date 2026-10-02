-- +goose NO TRANSACTION
-- +goose Up

-- Answers `tags["k"] == "v"` pushed down as `tags @> '{"k":"v"}'` — the
-- console's tag-facet filter. jsonb_path_ops rather than the default
-- jsonb_ops: containment is the only operator the query uses, and the
-- path-ops index is a fraction of the size. CONCURRENTLY and on its own,
-- per CONVENTIONS.md.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tags_gin
    ON objects USING gin (tags jsonb_path_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tags_gin;
