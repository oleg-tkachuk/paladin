-- +goose Up
-- +goose StatementBegin
-- 039_tenant_slug_history.sql
--
-- Durable history of tenant slug rotations. RenameTenantSlug rewrites the
-- live slug in place (migration 009 + the 033 rename trigger), so once an
-- operator renames `acme` → `acme-corp` every bookmark to `/tenants/acme/...`
-- 404s by design. To later offer a "did you mean acme-corp?" redirect on
-- such a 404 we need a record of (old_slug → new_slug); the audit_log keeps
-- one too, but it is platform-admin-gated and so cannot back a redirect
-- served to ordinary tenant members. This table is the resolver-friendly
-- source: a future ResolveRenamedSlug RPC reads it with tenant-read authz.
--
-- Written transactionally inside TenantRepo.Rename, so a row exists iff the
-- slug actually rotated (the idempotent same-slug no-op writes nothing).
-- ON DELETE CASCADE: when a tenant is hard-deleted its rename trail goes too.
CREATE TABLE IF NOT EXISTS tenant_slug_history (
    entry_id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  UUID        NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    old_slug   TEXT        NOT NULL,
    new_slug   TEXT        NOT NULL,
    renamed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Resolver lookup is "most recent rotation away FROM <old_slug>", optionally
-- bounded by a grace window on renamed_at — this composite serves both.
CREATE INDEX IF NOT EXISTS idx_tenant_slug_history_old_slug
    ON tenant_slug_history (old_slug, renamed_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tenant_slug_history;
-- +goose StatementEnd
