-- +goose Up
-- +goose StatementBegin
-- 054_tenant_storage_layout.sql
--
-- Per-tenant storage layout (ADR-0011). 'shared' (default) keeps a tenant's
-- objects inside a shared physical bucket, isolated by the <tenant_id>/ key
-- prefix — today's behaviour, so every existing tenant is 'shared'.
-- 'dedicated' gives the tenant its own physical bucket (buckets.owner_tenant_id
-- = the tenant), provisioned on CreateTenant. The column is an intent marker;
-- the isolation gate stays the enforce_object_key_bucket_tenancy trigger.
ALTER TABLE tenants
    ADD COLUMN storage_layout TEXT NOT NULL DEFAULT 'shared'
        CHECK (storage_layout IN ('shared', 'dedicated'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenants DROP COLUMN storage_layout;
-- +goose StatementEnd
