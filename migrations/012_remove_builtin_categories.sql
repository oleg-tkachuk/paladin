-- +goose NO TRANSACTION
-- +goose Up

-- Remove the seeded 'objects' category
DELETE FROM object_categories WHERE slug = 'objects' AND (tenant_id = 'default' OR tenant_id = 'test-tenant');

-- Remove the default 'objects' constraint from objects.category
-- First, identify the constraint name if any, but since it was added without a name 
-- in migration 010, it's just a DEFAULT value.
ALTER TABLE objects ALTER COLUMN category DROP DEFAULT;

-- +goose Down
-- Re-seed the default categories
INSERT INTO object_categories (tenant_id, slug, name, description)
VALUES 
  ('test-tenant', 'objects', 'Default Objects', 'Default fallback category'),
  ('default', 'objects', 'Default Objects', 'Default fallback category')
ON CONFLICT DO NOTHING;

-- Restore the default 'objects' constraint
ALTER TABLE objects ALTER COLUMN category SET DEFAULT 'objects';
