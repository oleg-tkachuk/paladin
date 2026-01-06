-- +goose Up
-- Add labels and external_ref to objects table
ALTER TABLE objects 
ADD COLUMN IF NOT EXISTS labels JSONB DEFAULT '{}'::jsonb,
ADD COLUMN IF NOT EXISTS external_ref TEXT NULL;

-- Index for efficient filtering on labels
CREATE INDEX IF NOT EXISTS idx_objects_labels ON objects USING GIN (labels);

-- Unique constraint for external_ref per tenant (enables idempotency)
CREATE UNIQUE INDEX IF NOT EXISTS uq_objects_tenant_external_ref ON objects (tenant_id, external_ref);

-- +goose Down
DROP INDEX IF EXISTS uq_objects_tenant_external_ref;
DROP INDEX IF EXISTS idx_objects_labels;
ALTER TABLE objects 
DROP COLUMN external_ref,
DROP COLUMN labels;
