-- +goose NO TRANSACTION
-- +goose Up

-- 064: objects keyset-pagination index.
--
-- ListObjects is the console's object browser and the busiest read on the
-- data plane:
--
--   WHERE tenant_id = $1 AND object_key = $2 [AND state = $3]
--     AND object_id > $after
--   ORDER BY object_id LIMIT n
--
-- Nothing served the ordering. The closest existing index,
-- idx_objects_object_key_committed (tenant_id, object_key, committed_at DESC),
-- gives the equality prefix but then hands the planner a set it has to SORT by
-- object_id — and the keyset `object_id > $after` degrades to a filter, so
-- paging deep into an ObjectKey re-reads and re-sorts everything before the
-- cursor. Cost grows with page number, which is the pathology keyset
-- pagination exists to avoid.
--
-- (tenant_id, object_key, object_id) turns that into a plain index range scan:
-- equality on the first two columns, range + ordering on the third, no sort
-- node, work proportional to the page rather than the offset. The same index
-- serves IterateObjectsForLifecycle, which walks the same tuple backwards
-- (`object_id < $cursor ORDER BY object_id DESC`) — a btree scans either
-- direction — and CountObjects' equality prefix.
--
-- Write cost is the reason this shape was chosen over adding `state`: all
-- three columns are immutable for the life of a row, so the index takes one
-- insert per object and is never touched again. A state-bearing index would
-- churn on every PENDING→AVAILABLE promote and every delete, making those
-- UPDATEs non-HOT — working directly against the fillfactor tuning migration
-- 008 applied for exactly that reason.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_keyset
    ON objects (tenant_id, object_key, object_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_keyset;
