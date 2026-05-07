-- +goose Up
-- +goose StatementBegin

-- ─── pgvector extension + vector_records table ──────────────────────────────
--
-- Day-1 vector index lives in the same Postgres as the rest of PALADIN. See
-- the rationale in internal/vector/pgvector/indexer.go: same operational
-- footprint, transactional consistency with object metadata, no extra
-- stateful component until volume forces the move to a dedicated store.
--
-- Dimension: 1536 covers OpenAI text-embedding-3-small and Voyage AI
-- voyage-3-lite without column changes; bge-large is 1024 (still fits the
-- column — pgvector stores actual length up to declared max). When a deploy
-- needs a larger dimension, add a new column or partition by model rather
-- than ALTER the column type, which rebuilds the HNSW index.
--
-- Natural key: (tenant_id, object_uri, chunk_ref, model). This is what
-- makes Upsert idempotent — re-embedding the same chunk replaces in place
-- instead of accumulating duplicates.
--
-- Tenant filter: every Search must include tenant_id. The btree index on
-- (tenant_id, model) is what makes that fast; HNSW alone can't enforce
-- tenancy.
--
-- HNSW parameters (m=16, ef_construction=64) are pgvector defaults that
-- balance build time and recall — operators tune by reindexing once the
-- per-tenant volume justifies it.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS vector_records (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    object_uri  text NOT NULL,
    chunk_ref   text NOT NULL DEFAULT '',
    model       text NOT NULL,
    embedding   vector(1536) NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT NOW(),
    updated_at  timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT vector_records_natural_key UNIQUE (tenant_id, object_uri, chunk_ref, model)
);

CREATE INDEX IF NOT EXISTS vector_records_tenant_model_idx
    ON vector_records (tenant_id, model);

CREATE INDEX IF NOT EXISTS vector_records_object_idx
    ON vector_records (tenant_id, object_uri);

-- HNSW index — cosine distance to match the adapter's metric.
CREATE INDEX IF NOT EXISTS vector_records_embedding_hnsw
    ON vector_records USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Per-tenant payload filtering: GIN on jsonb so MustMatch predicates use
-- the @> containment operator efficiently. Selective enough at modest
-- volume; partition by kind when one tenant's index outgrows it.
CREATE INDEX IF NOT EXISTS vector_records_payload_gin
    ON vector_records USING gin (payload jsonb_path_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vector_records;
-- Extension is intentionally NOT dropped: other deploys / databases on
-- the same cluster may still rely on it.
-- +goose StatementEnd
