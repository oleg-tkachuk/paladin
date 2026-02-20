-- +goose NO TRANSACTION
-- +goose Up

-- 1. Objects: Optimize list by tenant + created_at (common default sort)
-- Existing idx_objects_tenant_status_created covers filtering by status,
-- but a pure list by tenant + created_at benefits from this specific index.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_created 
ON objects (tenant_id, created_at DESC);

-- 2. Audit Logs: Optimize filtering by method
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_method_created 
ON audit_logs (tenant_id, method, created_at DESC);

-- 3. Audit Logs: Optimize global pruning (reaper)
-- Pruning deletes old logs globally, regardless of tenant.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_created_at 
ON audit_logs (created_at);

-- 4. Multipart Uploads: Optimize expired cleanup (reaper)
-- Reaper finds uploads where status='initiated' AND expires_at < NOW()
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_initiated_expires 
ON multipart_uploads (expires_at) 
WHERE status = 'initiated';

ANALYZE objects;
ANALYZE audit_logs;
ANALYZE multipart_uploads;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_method_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_created_at;
DROP INDEX CONCURRENTLY IF EXISTS idx_multipart_initiated_expires;
