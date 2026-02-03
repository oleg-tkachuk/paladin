-- Migration: 005_performance_indexes
-- Description: Add composite indexes for common query patterns to improve performance
-- Author: Performance Optimization Phase 3
-- Date: 2026-02-03

-- +goose NO TRANSACTION
-- +goose Up
-- Index for listing objects by tenant, status, and creation time (most common query pattern)
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_status_created 
ON objects(tenant_id, status, created_at DESC);
-- +goose StatementEnd

-- Index for external_ref lookups (idempotency checks)
-- Partial index since external_ref is often NULL
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_external_ref 
ON objects(tenant_id, external_ref) 
WHERE external_ref IS NOT NULL;
-- +goose StatementEnd

-- Index for pending object cleanup (reaper worker)
-- Partial index for efficiency since most objects are not pending
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_pending_expires 
ON objects(expires_at) 
WHERE status = 'pending' AND expires_at IS NOT NULL;
-- +goose StatementEnd

-- Index for multipart uploads by tenant and status
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_tenant_status 
ON multipart_uploads(tenant_id, status, created_at DESC);
-- +goose StatementEnd

-- Index for multipart upload_id lookups (very common)
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_multipart_upload_id 
ON multipart_uploads(upload_id);
-- +goose StatementEnd

-- Analyze tables to update statistics after index creation
ANALYZE objects;
ANALYZE multipart_uploads;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_status_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_external_ref;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_pending_expires;
DROP INDEX CONCURRENTLY IF EXISTS idx_multipart_tenant_status;
DROP INDEX CONCURRENTLY IF EXISTS idx_multipart_upload_id;
