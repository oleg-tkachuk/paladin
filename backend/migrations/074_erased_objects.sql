-- +goose Up
-- +goose StatementBegin

-- A record that an object existed and was permanently deleted. Without it an
-- erased id and one that never existed both read back as "not found", so a
-- batch permanent delete reissued after its worker died reported its first
-- run's erasures as failures. Written by the statement that removes the
-- object row (HardDeleteObject); kept as long as a terminal operation is (the operations
-- reaper's TTL), which bounds how late a reissue can be.
--
-- Only the identity and the collection are kept — not the key, not the
-- metadata — so a tombstone says nothing about the object beyond where it was
-- and that it was erased. The collection scopes the answer the way the batch
-- was authorised: an id erased elsewhere is not this batch's work.
CREATE TABLE erased_objects (
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    object_id     uuid NOT NULL,
    -- The collection goes with its tombstones: a reissue into a collection
    -- that no longer exists has nothing to find.
    collection_id uuid NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    erased_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, object_id)
);
CREATE INDEX erased_objects_erased_at_idx ON erased_objects (erased_at);
-- Deleting a collection cascades here; without this the cascade scans.
CREATE INDEX erased_objects_collection_idx ON erased_objects (collection_id);

ALTER TABLE erased_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE erased_objects FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON erased_objects FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE erased_objects;

-- +goose StatementEnd
