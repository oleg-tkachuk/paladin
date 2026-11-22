-- +goose Up

-- Re-files audit rows by the rule the insert now applies
-- (admindomain.TrailTenant): a row names a resource tenant other than its
-- actor's only when the call's authorization admitted it there. Rows written
-- before it took the tenant from the request name alone, and did not record
-- what was admitted, so a refused call cannot be told from one admitted and
-- then failed: every row with an error whose resource tenant is not its
-- actor's is cleared, and stays in its actor's trail with its resource name
-- unchanged. A row that succeeded is left as it is.
--
-- One statement, as 041: the retention purge bounds the table, an UPDATE
-- takes row locks only, and the index on resource_tenant_id serves the
-- WHERE clause.
UPDATE audit_log
SET resource_tenant_id = NULL
WHERE resource_tenant_id IS NOT NULL
  AND error_message IS NOT NULL
  AND resource_tenant_id IS DISTINCT FROM actor_tenant_id;

-- +goose Down
-- Nothing to undo: the cleared tenants were never admitted.
SELECT 1;
