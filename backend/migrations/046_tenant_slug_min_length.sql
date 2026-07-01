-- +goose Up
-- +goose StatementBegin
-- 046_tenant_slug_min_length.sql
--
-- Make the DB agree with the API on the tenant-slug floor.
--
-- The migration-009 CHECK `slug ~ '^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$'` accepts
-- a 1-char slug (the inner group is optional) — but ValidateTenantSlug
-- (internal/api/v1/apiutil/slug.go) requires 3..63 chars, the documented,
-- DNS-label-compatible format. So a direct DB insert could create a 1-char
-- slug the API would never allow. Add the explicit length floor so both
-- layers enforce 3..63 (the regex already caps the max at 63 and forbids the
-- exactly-2 case, so this only tightens the lower bound — no existing
-- API-created slug is affected; backfilled `t-<hex>` slugs are long).
ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_slug_format;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_slug_format CHECK (
        char_length(slug) BETWEEN 3 AND 63
        AND slug ~ '^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$'
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert to the migration-009 form (1-char slugs accepted by the DB again).
ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_slug_format;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_slug_format CHECK (
        slug ~ '^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$'
    );
-- +goose StatementEnd
