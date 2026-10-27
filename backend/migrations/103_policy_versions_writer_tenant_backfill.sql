-- +goose Up

-- Versions written before 102 carry a writer but not the writer's tenant, so
-- the console looked a platform admin up in the edited tenant and showed a
-- bare id. A user's subject is its id, which is unique across tenants, so the
-- users table names the tenant. A writer that is no user — an agent, or a
-- user since deleted — stays NULL.
UPDATE policy_versions pv
   SET written_by_tenant_id = u.tenant_id
  FROM users u
 WHERE pv.written_by_tenant_id IS NULL
   AND pv.written_by = u.id::text;

-- +goose Down
-- Nothing to undo: 102's Down drops the column.
SELECT 1;
