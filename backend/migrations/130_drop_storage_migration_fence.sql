-- +goose Up
-- +goose StatementBegin

-- A storage migration moves objects one by one (ADR-0051), so what decided on
-- which side of its rebind a write had landed goes: the catch-up's and the
-- rollback's records of what they copied (116, 118), the copies' source ETags
-- (120), and the rebind's times and the old rollback cursor.
DROP TABLE storage_migration_caught_up;
DROP TABLE storage_migration_copied_back;
DROP TABLE storage_migration_source_etags;
ALTER TABLE tenant_storage_migrations
    DROP COLUMN rebound_at,
    DROP COLUMN rebound_back_at,
    DROP COLUMN copy_started_at,
    DROP COLUMN rollback_cursor_at,
    DROP COLUMN rollback_cursor_id;

-- Every object with confirmed bytes is placed, and every pending upload is
-- anchored to the bucket its URL was signed for. 124 and 127 backfilled
-- both; a row a binary from before them wrote since is set the same way.
UPDATE objects o
   SET placement_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state <> 'PENDING'
   AND o.placement_bucket_id IS NULL;
UPDATE objects o
   SET upload_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state = 'PENDING'
   AND o.upload_bucket_id IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_storage_migrations
    ADD COLUMN rebound_at timestamptz,
    ADD COLUMN rebound_back_at timestamptz,
    ADD COLUMN copy_started_at timestamptz,
    ADD COLUMN rollback_cursor_at timestamptz,
    ADD COLUMN rollback_cursor_id uuid;
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
