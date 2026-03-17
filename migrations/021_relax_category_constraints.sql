-- +goose Up
-- Relax constraints on category slugs to support a wider range of S3-compatible characters.
-- This allows uppercase, lowercase, numbers, and common S3 symbols.

-- 1. Relax object_categories.slug constraint
ALTER TABLE object_categories 
  DROP CONSTRAINT IF EXISTS chk_object_categories_slug;

ALTER TABLE object_categories
  ADD CONSTRAINT chk_object_categories_slug 
  CHECK (
    slug ~ E'^[a-zA-Z0-9!#$&\'()*+,.:;=?@\\[\\]\\\\ _~-]+(/[a-zA-Z0-9!#$&\'()*+,.:;=?@\\[\\]\\\\ _~-]+)*$' 
    AND length(slug) <= 1024
  );

-- 2. Relax objects.category constraint
ALTER TABLE objects 
  DROP CONSTRAINT IF EXISTS chk_objects_category;

ALTER TABLE objects
  ADD CONSTRAINT chk_objects_category 
  CHECK (
    category ~ E'^[a-zA-Z0-9!#$&\'()*+,.:;=?@\\[\\]\\\\ _~-]+(/[a-zA-Z0-9!#$&\'()*+,.:;=?@\\[\\]\\\\ _~-]+)*$' 
    AND length(category) <= 1024
  );

-- +goose Down
-- Revert to hierarchical alphanumeric constraints from migration 017.
ALTER TABLE object_categories 
  DROP CONSTRAINT IF EXISTS chk_object_categories_slug;

ALTER TABLE object_categories
  ADD CONSTRAINT chk_object_categories_slug 
  CHECK (slug ~ '^[a-z0-9]([a-z0-9_-]*[a-z0-9])?(/[a-z0-9]([a-z0-9_-]*[a-z0-9])?)*$' AND length(slug) <= 256);

ALTER TABLE objects 
  DROP CONSTRAINT IF EXISTS chk_objects_category;

ALTER TABLE objects
  ADD CONSTRAINT chk_objects_category 
  CHECK (category ~ '^[a-z0-9]([a-z0-9_-]*[a-z0-9])?(/[a-z0-9]([a-z0-9_-]*[a-z0-9])?)*$' AND length(category) <= 256);
