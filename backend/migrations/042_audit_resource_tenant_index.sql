-- +goose Up

-- Serves a tenant's trail: actor_tenant_id = X OR resource_tenant_id = X,
-- newest first. The actor half already has idx_audit_log_tenant_at.
--
-- Not CONCURRENTLY, the one exception to CONVENTIONS.md here: Postgres cannot
-- build an index concurrently on a partitioned table, and audit_log's monthly
-- partitions are created at run time (worker.PartitionMaintainer), so no
-- static list of them exists to index one by one. Partial, so only rows that
-- name a tenant are read into it, which keeps the build — and the write lock
-- it holds on the partitions — short. Partitions created later inherit it.
CREATE INDEX IF NOT EXISTS idx_audit_log_resource_tenant_at
    ON audit_log (resource_tenant_id, at DESC)
    WHERE resource_tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_resource_tenant_at;
