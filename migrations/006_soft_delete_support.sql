-- +goose Up
-- Migration: 006_soft_delete_support
-- Description: Add soft_deleted and hard_deleted statuses to objects table
-- Author: Soft Delete Implementation
-- Date: 2026-02-09

-- 1. Update Object Status constraints to include soft_deleted and hard_deleted
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_status;
ALTER TABLE objects ADD CONSTRAINT chk_objects_status CHECK (status IN ('pending', 'uploading', 'uploaded', 'complete', 'aborted', 'deleted', 'error', 'soft_deleted', 'hard_deleted'));

-- 2. Migrate existing 'deleted' objects to 'hard_deleted'
-- We treat legacy 'deleted' as 'hard_deleted' because they were removed from S3.
UPDATE objects SET status = 'hard_deleted' WHERE status = 'deleted';

-- +goose Down
-- Revert status updates (best effort, as we can't distinguish original 'deleted' from 'hard_deleted')
UPDATE objects SET status = 'deleted' WHERE status = 'hard_deleted';
UPDATE objects SET status = 'deleted' WHERE status = 'soft_deleted';

-- Revert constraint
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_status;
ALTER TABLE objects ADD CONSTRAINT chk_objects_status CHECK (status IN ('pending', 'uploading', 'uploaded', 'complete', 'aborted', 'deleted', 'error'));
