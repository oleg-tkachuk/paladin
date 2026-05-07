-- +goose Up
-- +goose StatementBegin

-- Markers for the on-store AI workers (summarization + embedding).
-- Both are NULL by default; workers poll for AVAILABLE objects with a
-- NULL marker, do the work, and write the marker back.
--
-- Why dedicated columns rather than metadata-jsonb keys: the workers
-- need a cheap "find me the next batch" query. A partial index on a
-- NULL TEXT column is trivially small and keeps the scan free of every
-- AVAILABLE object that already has a summary.
--
-- summary holds the LLM-generated digest. Capped to TEXT (no length
-- limit at the column level) — the worker truncates per its own config.
-- embedding_indexed_at records when the Vector Indexer last accepted
-- this object. NULL means the embedding worker has not yet processed
-- it; non-NULL means it's safe to skip on the next sweep.

ALTER TABLE objects
    ADD COLUMN IF NOT EXISTS summary TEXT,
    ADD COLUMN IF NOT EXISTS embedding_indexed_at TIMESTAMPTZ;

-- Drives SummarizationWorker.batch query. Partial keeps it tight: only
-- AVAILABLE rows that still need work appear here, so the scan cost
-- collapses to O(backlog) rather than O(all-AVAILABLE).
CREATE INDEX IF NOT EXISTS idx_objects_pending_summary
    ON objects (object_id)
    WHERE state = 'AVAILABLE' AND summary IS NULL;

CREATE INDEX IF NOT EXISTS idx_objects_pending_embedding
    ON objects (object_id)
    WHERE state = 'AVAILABLE' AND embedding_indexed_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_objects_pending_embedding;
DROP INDEX IF EXISTS idx_objects_pending_summary;
ALTER TABLE objects
    DROP COLUMN IF EXISTS embedding_indexed_at,
    DROP COLUMN IF EXISTS summary;
-- +goose StatementEnd
