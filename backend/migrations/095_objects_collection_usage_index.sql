-- +goose NO TRANSACTION
-- +goose Up

-- A collection's usage — its available objects' count, bytes and newest
-- commit — is read on every GetCollection and ListCollections page. This
-- answers it from the index alone, over the available objects only.
-- CONCURRENTLY, and on its own, per CONVENTIONS.md: objects is written on
-- every upload.
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_collection_available_usage_idx
    ON objects (collection_id) INCLUDE (size_bytes, committed_at)
    WHERE state = 'AVAILABLE';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS objects_collection_available_usage_idx;
