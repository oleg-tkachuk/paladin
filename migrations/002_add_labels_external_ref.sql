-- Add labels and external_ref to objects table

ALTER TABLE objects 
ADD COLUMN labels JSONB DEFAULT '{}'::jsonb,
ADD COLUMN external_ref TEXT NULL;

-- Index for efficient filtering on labels
CREATE INDEX IF NOT EXISTS idx_objects_labels ON objects USING GIN (labels);

-- Unique constraint for external_ref per tenant (enables idempotency)
CREATE UNIQUE INDEX IF NOT EXISTS uq_objects_tenant_external_ref ON objects (tenant_id, external_ref);
