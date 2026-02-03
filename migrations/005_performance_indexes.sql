-- Migration: 005_performance_indexes
-- Description: Add composite indexes for common query patterns to improve performance
-- Author: Performance Optimization Phase 3
-- Date: 2026-02-03

-- Index for listing objects by tenant, status, and creation time (most common query pattern)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_status_created 
ON objects(tenant_id, status, created_at DESC);

-- Index for external_ref lookups (idempotency checks)
-- Partial index since external_ref is often NULL
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_external_ref 
ON objects(tenant_id, external_ref) 
WHERE external_ref IS NOT NULL;

-- Index for pending object cleanup (reaper worker)
-- Partial index for efficiency since most objects are not pending
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_pending_expires 
ON objects(expires_at) 
WHERE status = 'pending' AND expires_at IS NOT NULL;

-- Index for multipart uploads by tenant and status
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_tenant_status 
ON multipart_uploads(tenant_id, status, created_at DESC);

-- Index for multipart upload_id lookups (very common)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_upload_id 
ON multipart_uploads(upload_id);

-- Analyze tables to update statistics after index creation
ANALYZE objects;
ANALYZE multipart_uploads;
