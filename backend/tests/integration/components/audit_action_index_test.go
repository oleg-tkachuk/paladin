//go:build integration

// Closes the BACKLOG "AuditLog action-prefix index: planner validation" entry
// with evidence, not assumption. Migration 040 added
// idx_audit_log_action_at = (action text_pattern_ops, at DESC). This asserts,
// via EXPLAIN, which query shapes actually use it:
//
//   - PREFIX-RANGE  (action LIKE 'admin.Bucket%' AND at >= cutoff
//     ORDER BY at DESC LIMIT N): the planner rides
//     idx_audit_log_at, NOT the action index — a prefix spans
//     many distinct action values, so the (action, at) order
//     cannot produce a global at-DESC stream without a sort.
//     It is still an INDEX scan (no Seq Scan), which is what
//     the DoD asked to confirm.
//   - EXACT-ACTION  (action = '…' ORDER BY at DESC LIMIT N): here the index
//     earns its keep — a single action value means (action,
//     at DESC) yields the ordered page directly, no sort.
//
// Matches the BenchmarkAuditListPushdown finding: adding the index did not
// move prefix-range latency (the planner never picked it for that shape).
package components

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	idxTestRows        = 40_000
	idxTestMatchEveryN = 20 // 5% carry the exact action below
	idxTestExactAction = "admin.BucketService.CreateBucket"
	idxTestPrefix      = "admin.Bucket%"
)

// seedRecentAudit inserts rows whose `at` lands within the current month so
// they route into a real partition (the schema baseline (001_initial_schema.sql) created the current month),
// not the DEFAULT catch-all — keeping the EXPLAIN representative of hot data.
func seedRecentAudit(t *testing.T, ctx context.Context, pool interface {
	CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error)
}, n int) {
	t.Helper()
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Spread across however many hours we are into the month (>=1).
	spanHours := int(now.Sub(monthStart).Hours())
	if spanHours < 1 {
		spanHours = 1
	}
	i := 0
	src := pgx.CopyFromFunc(func() ([]any, error) {
		if i >= n {
			return nil, nil
		}
		action := "iam.Login"
		if i%idxTestMatchEveryN == 0 {
			action = idxTestExactAction
		}
		at := monthStart.Add(time.Duration(i%spanHours) * time.Hour)
		row := []any{uuid.Must(uuid.NewV7()), at, "svc", nil, "admin", action, "r"}
		i++
		return row, nil
	})
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"audit_log"},
		[]string{"id", "at", "actor_subject", "actor_tenant_id", "actor_audience", "action", "resource_name"},
		src); err != nil {
		t.Fatalf("copy audit rows: %v", err)
	}
}

func explain(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return b.String()
}

func TestAuditActionIndex_PlannerChoice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedRecentAudit(t, ctx, pool, idxTestRows)
	mustExec(t, ctx, pool, `ANALYZE audit_log`)

	cutoff := time.Now().UTC().AddDate(0, 0, -30)

	// Child indexes on a partitioned parent are auto-named
	// <partition>_<cols>_idx, so the parent names (idx_audit_log_at,
	// idx_audit_log_action_at) never appear in the plan — match the child
	// suffixes instead. The at-only child is "..._at_idx"; the action child
	// is "..._action_at_idx" (a superstring), so test the action one first.

	// 1. PREFIX-RANGE: must be index-driven (no Seq Scan), and rides the
	// at-ordered index per partition — a prefix spans many distinct actions,
	// so the (action, at) order cannot produce a global at-DESC stream.
	prefixPlan := explain(t, ctx, pool, `
		SELECT id FROM audit_log
		WHERE action LIKE $1 AND at >= $2
		ORDER BY at DESC, id DESC LIMIT 50`, idxTestPrefix, cutoff)
	t.Logf("prefix-range plan:\n%s", prefixPlan)
	if strings.Contains(prefixPlan, "Seq Scan") {
		t.Errorf("prefix-range query fell back to a Seq Scan:\n%s", prefixPlan)
	}
	if strings.Contains(prefixPlan, "action_at_idx") {
		t.Errorf("prefix-range unexpectedly used the action index (cannot serve at-DESC for a multi-action prefix):\n%s", prefixPlan)
	}
	if !strings.Contains(prefixPlan, "_at_idx") {
		t.Errorf("prefix-range did not use the at-ordered index:\n%s", prefixPlan)
	}

	// 2. EXACT-ACTION: where the action index earns its keep — a single
	// action value lets (action, at DESC) drive the per-partition scan.
	// Assert the action index is chosen (child suffix _action_at_idx).
	exactPlan := explain(t, ctx, pool, `
		SELECT id FROM audit_log
		WHERE action = $1 AND at >= $2
		ORDER BY at DESC, id DESC LIMIT 50`, idxTestExactAction, cutoff)
	t.Logf("exact-action plan:\n%s", exactPlan)
	if !strings.Contains(exactPlan, "action_at_idx") {
		t.Errorf("exact-action query did not use the action index — it is not earning its keep:\n%s", exactPlan)
	}
	// NOTE on the Incremental Sort both plans carry: it comes from the
	// `id DESC` cursor tiebreaker (no single-column index supplies it)
	// and the cross-partition merge — cheap (tens of kB, sub-ms here), and
	// inherent to the (at, id) cursor, not an index defect. We do NOT
	// assert sort-free for that reason.
}
