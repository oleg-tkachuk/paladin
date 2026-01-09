-- +goose Up
-- Enable RLS on tables
ALTER TABLE objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_uploads ENABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_parts ENABLE ROW LEVEL SECURITY;

-- Create function to automatically update updated_at timestamp
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Add triggers for updated_at
CREATE TRIGGER trg_objects_updated_at
  BEFORE UPDATE ON objects
  FOR EACH ROW
  EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_multipart_uploads_updated_at
  BEFORE UPDATE ON multipart_uploads
  FOR EACH ROW
  EXECUTE FUNCTION set_updated_at();

-- Add constraints for valid status values
-- NOTE: We use TEXT columns with CHECK constraints instead of ENUMs for easier migration management
ALTER TABLE objects
  ADD CONSTRAINT chk_objects_status CHECK (status IN ('pending', 'active', 'deleted'));

ALTER TABLE multipart_uploads
  ADD CONSTRAINT chk_multipart_uploads_status CHECK (status IN ('initiated', 'completed', 'aborted', 'expired'));

-- RLS Policies
-- We use a session variable 'app.tenant_id' to enforce tenant isolation.
-- This requires the application to set this variable in every transaction.

-- Policy for objects: Users can only see/modify rows where tenant_id matches their session
CREATE POLICY tenant_isolation_objects ON objects
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Policy for multipart_uploads
CREATE POLICY tenant_isolation_multipart_uploads ON multipart_uploads
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Policy for multipart_parts (implicit via multipart_uploads, but safer to be explicit if we join)
-- Since multipart_parts doesn't have tenant_id, we check via the join, or strictly relying on the parent UUID not being guessable.
-- However, RLS on joined tables can be complex. For 'multipart_parts', since we don't expose it directly without upload_id,
-- and upload_id lookup is protected by 'multipart_uploads' RLS, we can technically leave it or add a slightly more complex policy.
-- For simplest robust security, we can propagate tenant_id to multipart_parts or just rely on the parent check logic in app.
-- Given the prompt instructions "Enable RLS on objects/multipart tables (plural)", we focus on the main entities.
-- Let's add a basic "deny all unless via parent" strategy or just keep it enabled but open if not queried directly (which is risky).
-- Better approach: Add tenants to parts? No, that requires schema change not requested.
-- Alternative: WITH CHECK using valid join? Performance impact.
-- Decision: We enable RLS on parts but since we don't have tenant_id there, we rely on application logic + parent RLS for access control.
-- But wait, if someone guesses a multipart_id, they could Select * from multipart_parts?
-- Yes. To be strictly secure, we should add tenant_id to multipart_parts or use `USING (multipart_id IN (SELECT id FROM multipart_uploads))`
-- which respects the policy on multipart_uploads.
CREATE POLICY tenant_isolation_multipart_parts ON multipart_parts
  USING (multipart_id IN (SELECT id FROM multipart_uploads)); 

-- +goose Down
DROP POLICY IF EXISTS tenant_isolation_multipart_parts ON multipart_parts;
DROP POLICY IF EXISTS tenant_isolation_multipart_uploads ON multipart_uploads;
DROP POLICY IF EXISTS tenant_isolation_objects ON objects;

ALTER TABLE multipart_parts DISABLE ROW LEVEL SECURITY;
ALTER TABLE multipart_uploads DISABLE ROW LEVEL SECURITY;
ALTER TABLE objects DISABLE ROW LEVEL SECURITY;

ALTER TABLE multipart_uploads DROP CONSTRAINT IF EXISTS chk_multipart_uploads_status;
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_status;

DROP TRIGGER IF EXISTS trg_multipart_uploads_updated_at ON multipart_uploads;
DROP TRIGGER IF EXISTS trg_objects_updated_at ON objects;

DROP FUNCTION IF EXISTS set_updated_at;
