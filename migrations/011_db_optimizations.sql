-- +goose Up
-- 1. Redundant Index Removal
-- These indexes are either unused, duplicate coverage of other indexes, or enforce rules already handled by unique constraints.

-- Drop redundant category slug index (covered by uq_object_categories_tenant_slug)
DROP INDEX IF EXISTS idx_object_categories_tenant_slug;

-- Drop redundant multipart upload ID index (covered by uq_mpu_tenant_upload)
DROP INDEX CONCURRENTLY IF EXISTS idx_multipart_upload_id;

-- Drop redundant object tenant+created index (covered by idx_objects_tenant_created_at)
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_created;

-- Drop unused GIN labels index (write amplification with no read benefit)
DROP INDEX IF EXISTS idx_objects_labels;

-- 2. Audit Logs: Transition to BRIN for append-only time series data
-- Drop the large B-Tree index
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_created_at;

-- Create lightweight BRIN index for bulk time-based pruning
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_brin_created_at 
ON audit_logs USING BRIN (created_at);

-- 3. Audit Logs: Deterministic sort for pagination
-- Optimizes: ORDER BY created_at DESC, id DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_tenant_created_id 
ON audit_logs (tenant_id, created_at DESC, id DESC);

-- 4. Objects: Optimize Prefix Searches
-- B-Trees can't natively optimize 'LIKE prefix%' unless created with text_pattern_ops
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_key_prefix 
ON objects (tenant_id, object_key text_pattern_ops);

-- Re-analyze tables to update statistics after index overhauls
ANALYZE objects;
ANALYZE audit_logs;

-- +goose Down
-- Revert 4
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_key_prefix;

-- Revert 3
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_tenant_created_id;

-- Revert 2
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_brin_created_at;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_created_at ON audit_logs (created_at);

-- Revert 1
CREATE INDEX IF NOT EXISTS idx_objects_labels ON objects USING GIN (labels);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_created ON objects (tenant_id, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_upload_id ON multipart_uploads (upload_id);
CREATE INDEX IF NOT EXISTS idx_object_categories_tenant_slug ON object_categories (tenant_id, slug);
