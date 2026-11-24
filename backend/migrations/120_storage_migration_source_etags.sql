-- +goose Up
-- +goose StatementBegin

-- What a storage migration's copy read from the source, per object key: the
-- ETag the source reported just before the copy (or the catch-up) took the
-- object's bytes (ADR-0015). An overwrite written to the source after that
-- copy is recorded only when its storage event is; one whose event is late
-- leaves the source holding newer bytes than the target, and the cleanup
-- would delete them. The cleanup keeps a source copy whose ETag no longer
-- matches this one for an object whose last recorded write predates the
-- rebind: the late event moves it, and the next pass deletes it.
--
-- Keyed by the object's key, which its bytes are stored under, not its row.
-- Cleared when an attempt's copy starts, when the migration is cleaned, and
-- when a rollback rebinds the tenant back.
CREATE TABLE storage_migration_source_etags (
    tenant_id     uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    collection_id uuid        NOT NULL,
    path          text        NOT NULL,
    etag          text        NOT NULL,
    read_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, collection_id, path)
);

ALTER TABLE storage_migration_source_etags ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_migration_source_etags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storage_migration_source_etags FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE storage_migration_source_etags;

-- +goose StatementEnd
