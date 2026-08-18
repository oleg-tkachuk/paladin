-- +goose NO TRANSACTION
-- +goose Up

-- 067: operations keyset-pagination index.
--
-- ListOperations pages a tenant's async jobs the same way ListObjects pages
-- objects:
--
--   WHERE tenant_id = $1 [AND state = $2] AND operation_id > $after
--   ORDER BY operation_id LIMIT n
--
-- The existing idx_operations_tenant_state (tenant_id, state, created_at DESC)
-- was built for the "recent operations, filtered by state" view. It cannot
-- serve this ordering: `state` sits between the equality column and the sort
-- column, so an unfiltered listing (state is optional, and the console's
-- default view omits it) can only use the leading tenant_id and must sort the
-- rest. The keyset cursor degrades to a filter alongside it.
--
-- (tenant_id, operation_id) makes it a range scan. operation_id is a UUIDv7,
-- so its btree order is creation order — the cursor walks the tenant's
-- operations oldest-first with no sort, and the optional state predicate
-- becomes a cheap recheck over one page rather than a sort key.
--
-- Both columns are immutable; operations rows are updated in place as they
-- transition (state, done_at) but never change tenant or id, so this index
-- takes one insert and no update churn.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_operations_tenant_keyset
    ON operations (tenant_id, operation_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_operations_tenant_keyset;
