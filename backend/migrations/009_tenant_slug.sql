-- +goose Up
-- +goose StatementBegin

-- Tenant slug — operator-friendly stable string identifier.
--
-- Motivation:
--   The tenant_id is a UUID — fine for machines, awful in Cedar policies that
--   operators must read by hand. A "slug" gives every tenant a short kebab-case
--   handle (e.g. `acme`, `acme-prod`) that becomes the canonical Cedar
--   `Tenant::"…"` UID and may be substituted for the UUID anywhere a resource
--   name is expected (`tenants/acme`, `tenants/{uuid}` both resolve).
--
-- Properties:
--   - Required for every tenant (NOT NULL UNIQUE).
--   - Format constrained to kebab-case, 3..63 chars, must start with a letter,
--     must end with alphanumeric. Mirrors the validation in
--     internal/api/v1/apiutil/slug.go — keep both in lockstep.
--   - Backfilled for existing rows as `t-<hex(uuid)>` so the migration is safe
--     on populated databases. Operators can rename later via the slug-rename
--     admin RPC (see BACKLOG: "Tenant slug rename").
--   - Slug is intended to be immutable. The CHECK enforces format only — a
--     separate UPDATE policy controls who may rename and what side-effects
--     (Cedar policy rewrites) are required.

ALTER TABLE tenants
    ADD COLUMN slug TEXT;

-- Backfill — `t-` prefix keeps backfilled slugs visually distinct from
-- operator-chosen ones. UUIDs lose their dashes so the result fits the
-- kebab-case format check.
UPDATE tenants
SET slug = 't-' || replace(tenant_id::text, '-', '')
WHERE slug IS NULL;

ALTER TABLE tenants
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT tenants_slug_format CHECK (
        slug ~ '^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$'
    ),
    ADD CONSTRAINT tenants_slug_unique UNIQUE (slug);

CREATE INDEX idx_tenants_slug ON tenants (slug);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_tenants_slug;
ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_slug_unique,
    DROP CONSTRAINT IF EXISTS tenants_slug_format,
    DROP COLUMN IF EXISTS slug;

-- +goose StatementEnd
