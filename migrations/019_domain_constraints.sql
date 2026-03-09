-- +goose Up
-- +goose StatementBegin

-- 1. Tenant constraints
-- DisplayName max length: 64 symbols
ALTER TABLE tenants 
  ADD CONSTRAINT chk_tenants_display_name_length 
  CHECK (display_name IS NULL OR length(display_name) <= 64);

-- 2. Category constraints
-- Name unique per TenantID context
ALTER TABLE object_categories 
  ADD CONSTRAINT uq_object_categories_tenant_name 
  UNIQUE (tenant_id, name);

-- Name max length: 64 symbols
ALTER TABLE object_categories 
  ADD CONSTRAINT chk_object_categories_name_length 
  CHECK (length(name) <= 64);

-- Description max length: 128 symbols
ALTER TABLE object_categories 
  ADD CONSTRAINT chk_object_categories_description_length 
  CHECK (description IS NULL OR length(description) <= 128);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE object_categories DROP CONSTRAINT IF EXISTS chk_object_categories_description_length;
ALTER TABLE object_categories DROP CONSTRAINT IF EXISTS chk_object_categories_name_length;
ALTER TABLE object_categories DROP CONSTRAINT IF EXISTS uq_object_categories_tenant_name;
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS chk_tenants_display_name_length;
-- +goose StatementEnd
