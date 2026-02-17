-- +goose NO TRANSACTION
-- +goose Up

-- 1. Create audit_logs table
CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    request_id TEXT NULL,
    idempotency_key TEXT NULL,
    actor_subject TEXT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user','service','system')),
    client_ip INET NULL,
    user_agent TEXT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    query_params JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_body_sha256 TEXT NULL,
    request_size_bytes BIGINT NULL,
    
    -- Response metadata
    http_status INT NULL,
    response_code TEXT NULL,
    response_status TEXT NULL,
    response_time_ms INT NULL,
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. RLS Policy (Tenant Isolation)
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_audit_logs ON audit_logs
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- 3. Required Indexes (CONCURRENTLY)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_created_at ON audit_logs (tenant_id, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_path_created_at ON audit_logs (tenant_id, path, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_status_created_at ON audit_logs (tenant_id, http_status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_request_id ON audit_logs (tenant_id, request_id) WHERE request_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_idempotency_key ON audit_logs (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

ANALYZE audit_logs;

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
