-- +goose NO TRANSACTION
-- +goose Up

-- 1. Objects: Optimize category-filtered listings
-- Optimizes: WHERE tenant_id = $1 AND category = $2 ORDER BY created_at DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_category_created 
ON objects (tenant_id, category, created_at DESC);

-- 2. Audit Logs: Optimize prefix searches on paths
-- Optimizes: WHERE tenant_id = $1 AND path LIKE $2 || '%'
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_path_prefix 
ON audit_logs (tenant_id, path text_pattern_ops);

-- 3. Audit Logs: Optimize idempotency key lookups
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_idempotency 
ON audit_logs (tenant_id, idempotency_key) 
WHERE idempotency_key IS NOT NULL;

-- 4. Object Categories: Optimize category listings
-- Optimizes: WHERE tenant_id = $1 ORDER BY created_at DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_object_categories_tenant_created 
ON object_categories (tenant_id, created_at DESC);

-- Re-analyze tables to update statistics following index additions
ANALYZE objects;
ANALYZE audit_logs;
ANALYZE object_categories;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_category_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_path_prefix;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_idempotency;
DROP INDEX CONCURRENTLY IF EXISTS idx_object_categories_tenant_created;
