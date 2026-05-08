-- +goose Up
-- +goose StatementBegin

-- Paladin schema.
--
-- Clean-slate init: no object keys, no dual-addressing. object_id is UUIDv7
-- (sortable), resource_version is a monotonic BIGINT bumped by a trigger.

-- ─── Tenants ───────────────────────────────────────────────────────────────
CREATE TABLE tenants (
    tenant_id               UUID PRIMARY KEY,
    display_name            TEXT,
    labels                  JSONB NOT NULL DEFAULT '{}'::jsonb,
    inherited_cedar_policy  TEXT NOT NULL DEFAULT '',
    -- Compiled Cedar policy cache (binary); filled by worker, never by hand.
    inherited_policy_hash   BYTEA,
    resource_version        BIGINT NOT NULL DEFAULT 1,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tenants_labels_gin ON tenants USING GIN (labels);

-- ─── Storage backends (server config, mirrored for audit) ──────────────────
CREATE TABLE storage_backends (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,         -- 'aws-s3' | 's3-compatible' | 'gcs' | …
    endpoint       TEXT,
    region         TEXT,
    events_enabled BOOLEAN NOT NULL DEFAULT false,
    events_target  TEXT,                  -- 'sqs' | 'redis' | 'none'
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Buckets (logical namespaces) ──────────────────────────────────────────
CREATE TABLE object_keys (
    tenant_id          UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE RESTRICT,
    object_key          TEXT NOT NULL,
    display_name       TEXT,
    storage_backend    TEXT NOT NULL REFERENCES storage_backends(id),
    cedar_policy       TEXT NOT NULL DEFAULT '',
    cedar_policy_hash  BYTEA,
    lifecycle_rules    JSONB NOT NULL DEFAULT '[]'::jsonb,
    resource_version   BIGINT NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, object_key),
    CONSTRAINT object_key_format CHECK (object_key ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')
);

CREATE INDEX idx_object_keys_storage_backend ON object_keys(storage_backend);

-- ─── Objects ───────────────────────────────────────────────────────────────
CREATE TYPE object_state AS ENUM (
    'PENDING',
    'AVAILABLE',
    'FAILED',
    'DELETED'
);

CREATE TABLE objects (
    object_id          UUID PRIMARY KEY,              -- UUIDv7, sortable
    tenant_id          UUID NOT NULL,
    object_key          TEXT NOT NULL,
    key                TEXT NOT NULL,
    state              object_state NOT NULL,

    content_type       TEXT NOT NULL,
    size_bytes         BIGINT,                        -- NULL until AVAILABLE
    etag               TEXT,
    checksum_algorithm SMALLINT NOT NULL,             -- maps to enum value
    checksum           TEXT,
    -- S3 event sequencer. Used to resolve out-of-order event / RPC races.
    sequencer          TEXT,

    metadata           JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags               JSONB NOT NULL DEFAULT '{}'::jsonb,
    external_ref       TEXT,

    resource_version   BIGINT NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at       TIMESTAMPTZ,
    terminated_at      TIMESTAMPTZ,
    presign_expires_at TIMESTAMPTZ,

    FOREIGN KEY (tenant_id, object_key) REFERENCES object_keys(tenant_id, object_key) ON DELETE RESTRICT
);

-- Uniqueness: a given (object_key, key) may have at most one non-deleted row.
-- Historical (DELETED) rows are preserved for audit/restore.
CREATE UNIQUE INDEX ux_objects_live_key
    ON objects(tenant_id, object_key, key)
    WHERE state <> 'DELETED';

CREATE INDEX idx_objects_state_pending_expiry
    ON objects(presign_expires_at)
    WHERE state = 'PENDING';

CREATE INDEX idx_objects_object_key_committed
    ON objects(tenant_id, object_key, committed_at DESC NULLS LAST);

CREATE INDEX idx_objects_tags_gin ON objects USING GIN (tags jsonb_path_ops);
CREATE INDEX idx_objects_metadata_gin ON objects USING GIN (metadata jsonb_path_ops);

-- ─── Multipart upload sessions ─────────────────────────────────────────────
CREATE TABLE multipart_uploads (
    upload_id         TEXT PRIMARY KEY,
    object_id         UUID NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE,
    storage_upload_id TEXT NOT NULL,                  -- Opaque from S3
    part_size_bytes   BIGINT NOT NULL,
    total_parts       INT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE multipart_parts (
    upload_id   TEXT NOT NULL REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE,
    part_number INT NOT NULL,
    size_bytes  BIGINT NOT NULL,
    etag        TEXT NOT NULL,
    checksum    TEXT,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, part_number)
);

-- ─── Long-running operations ───────────────────────────────────────────────
CREATE TYPE operation_state AS ENUM (
    'PENDING',
    'RUNNING',
    'SUCCEEDED',
    'FAILED',
    'CANCELLED'
);

CREATE TABLE operations (
    operation_id  UUID PRIMARY KEY,                   -- UUIDv7
    tenant_id     UUID NOT NULL,
    type          TEXT NOT NULL,                      -- 'BatchDelete', 'BatchCopy', …
    state         operation_state NOT NULL DEFAULT 'PENDING',
    -- Protobuf Any, base64-encoded.
    metadata      BYTEA,
    response      BYTEA,
    error_code    TEXT,
    error_message TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at       TIMESTAMPTZ
);

CREATE INDEX idx_operations_tenant_state ON operations(tenant_id, state, created_at DESC);

-- ─── Idempotency keys (HTTP Idempotency-Key header) ────────────────────────
-- Keys scoped per (tenant, rpc method).
CREATE TABLE idempotency_keys (
    tenant_id    UUID NOT NULL,
    method       TEXT NOT NULL,
    key          TEXT NOT NULL,
    response     BYTEA NOT NULL,
    response_sha BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, method, key)
);

CREATE INDEX idx_idempotency_keys_expiry ON idempotency_keys(expires_at);

-- +goose StatementEnd

-- ─── Resource-version bump trigger (monotonic) ─────────────────────────────
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_resource_version() RETURNS trigger AS $$
BEGIN
    IF NEW.resource_version IS NULL OR NEW.resource_version = OLD.resource_version THEN
        NEW.resource_version := OLD.resource_version + 1;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_tenants_bump_rv
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

CREATE TRIGGER trg_object_keys_bump_rv
    BEFORE UPDATE ON object_keys
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

CREATE TRIGGER trg_objects_bump_rv
    BEFORE UPDATE ON objects
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_objects_bump_rv ON objects;
DROP TRIGGER IF EXISTS trg_object_keys_bump_rv ON object_keys;
DROP TRIGGER IF EXISTS trg_tenants_bump_rv ON tenants;
DROP FUNCTION IF EXISTS bump_resource_version();

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS operations;
DROP TYPE IF EXISTS operation_state;
DROP TABLE IF EXISTS multipart_parts;
DROP TABLE IF EXISTS multipart_uploads;
DROP TABLE IF EXISTS objects;
DROP TYPE IF EXISTS object_state;
DROP TABLE IF EXISTS object_keys;
DROP TABLE IF EXISTS storage_backends;
DROP TABLE IF EXISTS tenants;
-- +goose StatementEnd
