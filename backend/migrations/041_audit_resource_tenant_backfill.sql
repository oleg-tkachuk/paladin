-- +goose Up

-- Fills resource_tenant_id for rows written before 040, by the rule
-- apiutil.TenantInResourceName applies on insert: the value after the first
-- `tenants` segment of resource_name, when it is a UUID. A slug there is left
-- NULL, as it is on insert.
--
-- One statement rather than batches: it touches only rows whose name carries
-- a tenant, the retention purge bounds the table, and an UPDATE takes row
-- locks only — reads and new inserts proceed while it runs. The WHERE clause
-- admits a value only once it is a UUID, so the cast cannot fail.
UPDATE audit_log
SET resource_tenant_id = substring(resource_name from '(?:^|/)tenants/([^/]*)')::uuid
WHERE resource_tenant_id IS NULL
  AND substring(resource_name from '(?:^|/)tenants/([^/]*)')
      ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

-- +goose Down
-- Nothing to undo: 040's Down drops the column.
SELECT 1;
