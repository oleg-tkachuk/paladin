-- +goose Up
-- +goose StatementBegin

-- A storage migration's copy walks the tenant's objects once, in (collection,
-- path) order, while the tenant keeps writing to the source. A write the
-- cursor has passed reached only the source: a new object, missing from the
-- target, or an overwrite, stale there. Before the rebind the migration now
-- catches up on every object whose bytes were written since the copy started
-- (ADR-0015).
--
-- copy_started_at is when the copy started: taken under the migration row's
-- lock, which every promote holds while the migration provisions
-- (migstate.FencingPromotes), so every object written before it was promoted
-- before the copy listed anything, and every later write is stamped after it.
-- NULL on a row from before this file; the worker reads created_at for it,
-- earlier than any copy of the row.
ALTER TABLE tenant_storage_migrations ADD COLUMN copy_started_at timestamptz;

-- One row per object the catch-up copied: the write (bytes_written_at) whose
-- bytes the copy took, and the ETag the target reported for the copy. An
-- object whose bytes_written_at is past the copy's start and differs from
-- the row's written_at has a write the target has not seen. The copy holds
-- the object's row while it copies (FOR KEY SHARE), which a promote's FOR
-- UPDATE waits on, so the written_at recorded is the one the copied bytes
-- belong to, or an older one that is copied again.
--
-- Cleared when an attempt starts its copy and when it completes; it holds the
-- objects written during one attempt's copy, never the tenant's whole store.
-- etag is NULL where the worker has no way to read one.
CREATE TABLE storage_migration_caught_up (
    tenant_id  uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    object_id  uuid        NOT NULL,
    written_at timestamptz NOT NULL,
    etag       text,
    copied_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, object_id)
);

ALTER TABLE storage_migration_caught_up ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_migration_caught_up FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storage_migration_caught_up FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE storage_migration_caught_up;
ALTER TABLE tenant_storage_migrations DROP COLUMN copy_started_at;

-- +goose StatementEnd
