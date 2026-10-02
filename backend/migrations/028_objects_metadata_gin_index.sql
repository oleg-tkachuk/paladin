-- +goose NO TRANSACTION
-- +goose Up

-- Answers `metadata["k"] == "v"` pushed down as `metadata @> '{"k":"v"}'`.
-- Same shape and reasoning as 027.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_metadata_gin
    ON objects USING gin (metadata jsonb_path_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_metadata_gin;
