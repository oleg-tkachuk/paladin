-- +goose Up
-- +goose StatementBegin
-- 052_tenant_read_own_policy.sql
--
-- The default inherited Cedar policy granted no Action::"ReadTenant" at all,
-- so any non-platform-admin principal — including tenant.admin — was denied
-- reading its OWN tenant's record (confirmed live: TenantService/GetTenant →
-- 403 "denied by policy"; the handler allows own-tenant reads, Cedar's
-- deny-by-default then rejects). The Go default template + the example
-- policy gain a member-level ReadTenant permit; this migration backfills the
-- same permit into EXISTING tenants' inherited_cedar_policy.
--
-- Idempotent + custom-policy-respecting: a tenant whose policy already
-- mentions Action::"ReadTenant" (either a previous run of this backfill or
-- an operator-authored rule) is left untouched.
--
-- inherited_policy_hash mirrors the Go side's sha256(policy_text) (it is
-- descriptive, never verified — but keep it consistent). resource_version is
-- bumped so OCC callers see the change; the UPDATE also fires migration
-- 051's policy_changed trigger, invalidating live engines' compiled caches.
UPDATE tenants
   SET inherited_cedar_policy = inherited_cedar_policy || E'\n' ||
        E'// The tenant record itself — any member may read their own tenant''s\n' ||
        E'// metadata (backfilled by migration 052; the default template now\n' ||
        E'// includes this permit for new tenants). ManageTenant is deliberately\n' ||
        E'// not granted — tenant lifecycle stays a platform concern.\n' ||
        E'permit (\n' ||
        E'    principal,\n' ||
        E'    action == Action::"ReadTenant",\n' ||
        E'    resource\n' ||
        E') when {\n' ||
        E'    principal.roles.contains("platform.admin") ||\n' ||
        E'    principal.tenant_id == resource.tenant_id\n' ||
        E'};\n',
       inherited_policy_hash = sha256(convert_to(
           inherited_cedar_policy || E'\n' ||
           E'// The tenant record itself — any member may read their own tenant''s\n' ||
           E'// metadata (backfilled by migration 052; the default template now\n' ||
           E'// includes this permit for new tenants). ManageTenant is deliberately\n' ||
           E'// not granted — tenant lifecycle stays a platform concern.\n' ||
           E'permit (\n' ||
           E'    principal,\n' ||
           E'    action == Action::"ReadTenant",\n' ||
           E'    resource\n' ||
           E') when {\n' ||
           E'    principal.roles.contains("platform.admin") ||\n' ||
           E'    principal.tenant_id == resource.tenant_id\n' ||
           E'};\n', 'UTF8')),
       resource_version = resource_version + 1,
       updated_at = NOW()
 WHERE inherited_cedar_policy IS NOT NULL
   AND inherited_cedar_policy <> ''
   AND inherited_cedar_policy NOT LIKE '%Action::"ReadTenant"%';
-- +goose StatementEnd

-- +goose Down
-- No down: removing a permit from operator-visible policy text
-- programmatically risks clobbering rules an operator has since edited.
