-- +goose Up

-- 1. Update Object Status constraints
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_status;
ALTER TABLE objects ADD CONSTRAINT chk_objects_status CHECK (status IN ('pending', 'uploading', 'uploaded', 'complete', 'aborted', 'deleted', 'error'));

-- 2. Add new columns to objects table for v1.1.0
ALTER TABLE objects ADD COLUMN IF NOT EXISTS stored_etag TEXT;
ALTER TABLE objects ADD COLUMN IF NOT EXISTS stored_size_bytes BIGINT;
ALTER TABLE objects ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;
ALTER TABLE objects ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

-- 3. Update Multipart Status constraints
ALTER TABLE multipart_uploads DROP CONSTRAINT IF EXISTS chk_multipart_uploads_status;
ALTER TABLE multipart_uploads ADD CONSTRAINT chk_multipart_uploads_status CHECK (status IN ('initiated', 'completed', 'aborted', 'expired', 'uploaded'));

-- 4. Idempotency table to store results of operations
CREATE TABLE IF NOT EXISTS idempotency_keys (
    tenant_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_path TEXT NOT NULL,
    request_hash TEXT NOT NULL, -- SHA256 of the request body
    response_code INTEGER NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_key)
);

-- Enable RLS on idempotency_keys
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_idempotency ON idempotency_keys
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- +goose Down
DROP POLICY IF EXISTS tenant_isolation_idempotency ON idempotency_keys;
DROP TABLE IF EXISTS idempotency_keys;

-- Revert columns
ALTER TABLE objects DROP COLUMN IF EXISTS stored_etag;
ALTER TABLE objects DROP COLUMN IF EXISTS stored_size_bytes;
ALTER TABLE objects DROP COLUMN IF EXISTS completed_at;
ALTER TABLE objects DROP COLUMN IF EXISTS deleted_at;

-- Revert constraints (approximate, won't restore old data if status changed)
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_status;
ALTER TABLE objects ADD CONSTRAINT chk_objects_status CHECK (status IN ('pending', 'active', 'deleted'));

ALTER TABLE multipart_uploads DROP CONSTRAINT IF EXISTS chk_multipart_uploads_status;
ALTER TABLE multipart_uploads ADD CONSTRAINT chk_multipart_uploads_status CHECK (status IN ('initiated', 'completed', 'aborted', 'expired'));
