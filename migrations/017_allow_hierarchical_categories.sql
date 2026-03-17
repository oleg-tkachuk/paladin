-- +goose Up
-- Allow forward slashes in category slugs for hierarchical organization.
-- Ensures no leading/trailing slashes and no consecutive slashes.
ALTER TABLE object_categories 
  DROP CONSTRAINT IF EXISTS chk_object_categories_slug;

ALTER TABLE object_categories
  ADD CONSTRAINT chk_object_categories_slug 
  CHECK (slug ~ '^[a-z0-9]([a-z0-9_-]*[a-z0-9])?(/[a-z0-9]([a-z0-9_-]*[a-z0-9])?)*$' AND length(slug) <= 256);

-- Update objects table constraint as well
ALTER TABLE objects 
  DROP CONSTRAINT IF EXISTS chk_objects_category;

ALTER TABLE objects
  ADD CONSTRAINT chk_objects_category 
  CHECK (category ~ '^[a-z0-9]([a-z0-9_-]*[a-z0-9])?(/[a-z0-9]([a-z0-9_-]*[a-z0-9])?)*$' AND length(category) <= 256);

-- +goose Down
ALTER TABLE object_categories 
  DROP CONSTRAINT IF EXISTS chk_object_categories_slug;

ALTER TABLE object_categories
  ADD CONSTRAINT chk_object_categories_slug 
  CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,62}$');

ALTER TABLE objects 
  DROP CONSTRAINT IF EXISTS chk_objects_category;

ALTER TABLE objects
  ADD CONSTRAINT chk_objects_category 
  CHECK (category ~ '^[a-z0-9][a-z0-9_-]{0,62}$');
