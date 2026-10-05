-- +goose Up

-- The tenant an audited action was done to, beside the tenant whose principal
-- did it (actor_tenant_id). A platform admin working inside tenant X leaves
-- rows whose actor is the platform tenant; this column is what puts them in
-- X's trail. Derived from resource_name on insert (AuditEntry.ResourceTenant);
-- NULL when the name carries no tenant.
--
-- Nullable with no default, so this is a catalog change only (CONVENTIONS.md).
-- Rows written before it are filled by 041.
ALTER TABLE audit_log ADD COLUMN resource_tenant_id uuid;

-- +goose Down
ALTER TABLE audit_log DROP COLUMN IF EXISTS resource_tenant_id;
