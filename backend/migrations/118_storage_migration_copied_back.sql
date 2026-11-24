-- +goose Up
-- +goose StatementBegin

-- A storage migration's rollback copies back to the source every object
-- whose bytes were written since the rebind. It walked them behind a
-- (written at, id) cursor; a promote stamps its write before it commits, so
-- one that committed after a batch had listed past its stamp fell behind the
-- cursor and was never copied back (ADR-0015). The rollback now records the
-- write each copy back took, as the catch-up does before the rebind
-- (storage_migration_caught_up, migration 116), and lists what it has not
-- copied rather than what is past a cursor.
--
-- One row per object: the write (COALESCE(bytes_written_at, updated_at))
-- the copy back took, or decided to leave, under the object's row hold
-- (FOR KEY SHARE, which a promote's FOR UPDATE waits on). An object whose
-- latest write differs from the row's written_at is copied again. Cleared
-- when a rollback starts and when it rebinds the tenant back.
--
-- tenant_storage_migrations.rollback_cursor_at and rollback_cursor_id are no
-- longer read; they stay until no running binary selects them (BACKLOG).
CREATE TABLE storage_migration_copied_back (
    tenant_id  uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    object_id  uuid        NOT NULL,
    written_at timestamptz NOT NULL,
    copied_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, object_id)
);

ALTER TABLE storage_migration_copied_back ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_migration_copied_back FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storage_migration_copied_back FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE storage_migration_copied_back;

-- +goose StatementEnd
