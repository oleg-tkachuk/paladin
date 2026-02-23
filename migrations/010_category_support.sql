-- +goose NO TRANSACTION
-- +goose Up

-- 1. Categories table (tenant-scoped, RLS-protected)
--    Stores the list of allowed categories per tenant.
--    Slugs are validated via CHECK constraint; uniqueness enforced per (tenant_id, slug).
CREATE TABLE object_categories (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   TEXT        NOT NULL,
  slug        TEXT        NOT NULL,   -- [a-z0-9][a-z0-9_-]{0,62}
  name        TEXT        NOT NULL,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_object_categories_tenant_slug UNIQUE (tenant_id, slug),
  CONSTRAINT chk_object_categories_slug CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,62}$')
);

-- RLS: tenants only see their own categories
ALTER TABLE object_categories ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_object_categories ON object_categories
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE INDEX idx_object_categories_tenant_slug
  ON object_categories (tenant_id, slug);

-- auto-update updated_at
CREATE TRIGGER trg_object_categories_updated_at
  BEFORE UPDATE ON object_categories
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Seed default categories for testing and common usage
INSERT INTO object_categories (tenant_id, slug, name, description)
VALUES 
  ('test-tenant', 'objects', 'Default Objects', 'Default fallback category'),
  ('default', 'objects', 'Default Objects', 'Default fallback category')
ON CONFLICT DO NOTHING;

-- 2. Add category + subpath columns to objects
ALTER TABLE objects
  ADD COLUMN category TEXT NOT NULL DEFAULT 'objects',
  ADD COLUMN subpath  TEXT DEFAULT NULL;

-- 3. Category slug format validation
ALTER TABLE objects
  ADD CONSTRAINT chk_objects_category CHECK (
    category ~ '^[a-z0-9][a-z0-9_-]{0,62}$'
  );

-- 4. Subpath validation: no '..' traversal, no leading/trailing slashes, max 256 chars
ALTER TABLE objects
  ADD CONSTRAINT chk_objects_subpath CHECK (
    subpath IS NULL OR (
      length(subpath) <= 256
      AND subpath NOT LIKE '%..%'
      AND subpath NOT LIKE '//%'
      AND subpath !~ '^/'
      AND subpath !~ '/$'
    )
  );

-- 5. Trigger: enforce object_key = tenant_id/category[/subpath]/id on INSERT
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_object_key()
RETURNS TRIGGER AS $$
BEGIN
  IF NEW.subpath IS NOT NULL THEN
    NEW.object_key := NEW.tenant_id || '/' || NEW.category || '/' || NEW.subpath || '/' || NEW.id::text;
  ELSE
    NEW.object_key := NEW.tenant_id || '/' || NEW.category || '/' || NEW.id::text;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_objects_enforce_key
  BEFORE INSERT ON objects
  FOR EACH ROW EXECUTE FUNCTION enforce_object_key();

-- 6. Indexes for category-aware access patterns
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_category_created
  ON objects (tenant_id, category, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_category_status_created
  ON objects (tenant_id, category, status, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_tenant_category_extref
  ON objects (tenant_id, category, external_ref)
  WHERE external_ref IS NOT NULL;

ANALYZE objects;
ANALYZE object_categories;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_category_extref;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_category_status_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_tenant_category_created;
DROP TRIGGER  IF EXISTS trg_objects_enforce_key ON objects;
DROP FUNCTION IF EXISTS enforce_object_key;
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_subpath;
ALTER TABLE objects DROP CONSTRAINT IF EXISTS chk_objects_category;
ALTER TABLE objects DROP COLUMN IF EXISTS subpath;
ALTER TABLE objects DROP COLUMN IF EXISTS category;
DROP TRIGGER  IF EXISTS trg_object_categories_updated_at ON object_categories;
DROP POLICY   IF EXISTS tenant_isolation_object_categories ON object_categories;
DROP TABLE    IF EXISTS object_categories;
