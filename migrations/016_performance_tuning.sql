-- +goose NO TRANSACTION
-- +goose Up

-- 1. Objects: Optimize prefix search on object keys
-- Optimizes: WHERE tenant_id = $1 AND object_key LIKE $2 || '%'
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_key_prefix 
ON objects (tenant_id, object_key text_pattern_ops);

-- 2. Objects: Optimize metadata filtering
-- Optimizes: WHERE labels @> $1
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_labels 
ON objects USING GIN (labels);

-- 3. Audit Logs: Optimize filtering by HTTP status
-- Optimizes: WHERE tenant_id = $1 AND http_status = $2 ORDER BY created_at DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_status_created 
ON audit_logs (tenant_id, http_status, created_at DESC);

-- 4. Audit Logs: Optimize request ID lookups
-- Optimizes: WHERE tenant_id = $1 AND request_id = $2
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_request_id 
ON audit_logs (tenant_id, request_id);

-- 5. Tenants: Optimize listing by creation time
-- Optimizes: ORDER BY created_at DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tenants_created_at_desc 
ON tenants (created_at DESC);

-- Re-analyze tables to update statistics following index additions
ANALYZE objects;
ANALYZE audit_logs;
ANALYZE tenants;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_key_prefix;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_labels;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_status_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_request_id;
DROP INDEX CONCURRENTLY IF EXISTS idx_tenants_created_at_desc;
