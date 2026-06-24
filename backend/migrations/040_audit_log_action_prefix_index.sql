-- +goose NO TRANSACTION
-- +goose Up

-- 040: audit_log action-prefix index.
--
-- ListAuditLog (and its CEL pushdown) filters by an action prefix ordered by
-- recency: `action LIKE 'admin.TenantService.%' AND at >= $since ORDER BY at
-- DESC LIMIT N`. Without an index on `action` the prefix is a filter, not a
-- range — the planner rides idx_audit_log_at for the ordering but re-checks the
-- prefix on every row, which the pushdown benchmark
-- (internal/integration/audit_bench_test.go) showed leaves latency within noise
-- on a 1M-row table.
--
-- text_pattern_ops makes the LIKE-prefix a range scan; the trailing `at DESC`
-- lets the same index serve exact-action newest-N queries without a sort, and
-- bounds the work for prefix ranges to the matching action span. Built
-- CONCURRENTLY (migrations/CONVENTIONS.md lock rules) so a populated audit_log
-- rides this without blocking writes; NO TRANSACTION because CONCURRENTLY can't
-- run inside one.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_action_at
    ON audit_log (action text_pattern_ops, at DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_action_at;
