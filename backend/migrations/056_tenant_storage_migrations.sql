-- +goose Up
-- +goose StatementBegin
-- 056_tenant_storage_migrations.sql
--
-- ADR-0011 Phase 3: the shared->dedicated storage migration copy job. When an
-- operator switches a tenant to the `dedicated` layout, its existing objects
-- must be copied from the shared bucket into the tenant's own bucket, then the
-- object_keys rebound. This table is the crash-safe, resumable state machine
-- the StorageMigrationWorker drives — one row per tenant migration.
--
-- State machine (forward-only; `failed` is terminal-but-retriable by the RPC):
--   provisioning -> copying -> rebinding -> verifying -> completed
--                \-------------- failed <-------------/
--
-- The cursor (cursor_object_key, cursor_key) is the last (object_key, key)
-- successfully copied, so a worker restart resumes mid-copy instead of
-- re-copying the whole prefix. Copies are idempotent (same key, server-side
-- CopyObject), so a small overlap on resume is harmless.
CREATE TABLE tenant_storage_migrations (
    tenant_id          UUID PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,

    -- Physical source (shared) and target (dedicated) the copy runs between.
    -- Slice 1 supports same-backend only; a cross-backend pair is rejected by
    -- the worker until the stream-through path lands (later slice).
    source_backend_id  TEXT NOT NULL,
    source_bucket_name TEXT NOT NULL,
    target_backend_id  TEXT NOT NULL,
    target_bucket_name TEXT NOT NULL,

    state TEXT NOT NULL DEFAULT 'provisioning'
        CHECK (state IN ('provisioning', 'copying', 'rebinding', 'verifying', 'completed', 'failed')),

    objects_total  BIGINT NOT NULL DEFAULT 0,
    objects_copied BIGINT NOT NULL DEFAULT 0,

    -- Resume cursor: the last (object_key, key) copied. Empty = not started.
    cursor_object_key TEXT NOT NULL DEFAULT '',
    cursor_key        TEXT NOT NULL DEFAULT '',

    error    TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

-- Worker scan: active (non-terminal) migrations, oldest-touched first.
CREATE INDEX idx_tenant_storage_migrations_active
    ON tenant_storage_migrations (updated_at)
    WHERE state NOT IN ('completed', 'failed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tenant_storage_migrations;
-- +goose StatementEnd
