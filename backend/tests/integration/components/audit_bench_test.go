//go:build integration

package components

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

const (
	benchAuditMatchPrefix = "admin.Bucket"
	benchAuditRows        = 1_000_000
	benchAuditSpanHours   = 90 * 24 // 90 days
	benchAuditMatchEveryN = 20      // ~5% of rows carry the match prefix
)

// benchAnchor is a fixed wall-clock so the seeded `at` distribution (and the
// 30-day cutoff derived from it) is deterministic across runs.
var benchAnchor = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// seedAuditRows streams n audit_log rows in via COPY (CopyFromFunc keeps
// memory flat). ~1/everyN rows carry an action under matchPrefix; `at` is
// spread back across spanHours from benchAnchor so a time-range predicate is
// selective. before/after JSON are left NULL — the bench isolates the
// predicate path, not payload width.
func seedAuditRows(b testing.TB, ctx context.Context, pool interface {
	CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error)
}, n int) {
	b.Helper()
	i := 0
	src := pgx.CopyFromFunc(func() ([]any, error) {
		if i >= n {
			return nil, nil
		}
		action := "iam.Login"
		if i%benchAuditMatchEveryN == 0 {
			action = benchAuditMatchPrefix + "Service.CreateBucket"
		}
		at := benchAnchor.Add(-time.Duration(i%benchAuditSpanHours) * time.Hour)
		row := []any{uuid.Must(uuid.NewV7()), at, "svc", nil, "admin", action, "r"}
		i++
		return row, nil
	})
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"audit_log"},
		[]string{"id", "at", "actor_subject", "actor_tenant_id", "actor_audience", "action", "resource_name"},
		src); err != nil {
		b.Fatalf("copy audit rows: %v", err)
	}
}

// BenchmarkAuditListPushdown quantifies the CEL-pushdown win on a
// representative `action.startsWith(prefix) && at >= cutoff` filter over 1M
// audit rows (BACKLOG "AuditLog CEL pushdown — production benchmark"). Run:
//
//		go test -tags=integration -run=^$ -bench=BenchmarkAuditListPushdown \
//		    -benchtime=20x ./tests/integration/components/...
//
//	  - pushdown:    repo.List populates ActionPrefix + AtGTE, so Postgres
//	    narrows via idx_audit_log_at + the action LIKE and ships ~one page.
//	  - no_pushdown: the pre-pushdown shape — fetch the candidate set with no
//	    SQL predicate and apply the predicate in Go, paging until a full page
//	    of matches is collected (what the in-memory CEL pass had to chew
//	    through). This understates the real gap, which also avoids a compiled
//	    CEL eval per shipped row.
//
// Compare the two ns/op; the ratio is the documented speedup.
//
// MEASURED (Apple M3 Max, 1M rows, 5% match density, postgres:17-alpine):
//
//	pushdown      1.79 ms/op     77 KB/op    285 allocs/op
//	no_pushdown   1.60 ms/op   1511 KB/op   5032 allocs/op
//
// FINDING — the original BACKLOG claim of "≥10× p50 latency speedup" is
// DROPPED: on this shape the two are within noise on latency. What pushdown
// DOES buy, decisively, is ~20× fewer bytes + allocations shipped into Go
// (77 KB vs 1.5 MB) — only the matching page crosses the wire instead of the
// full candidate set — plus, in production, one avoided compiled-CEL eval per
// shipped row (not modelled here, which understates the win).
//
// FOLLOW-UP RESOLVED (the schema baseline (001_initial_schema.sql) + partitioning 041). Migration 040 added
// idx_audit_log_action_at = (action text_pattern_ops, at DESC). Re-running
// this bench WITH that index did NOT move prefix-range latency (pushdown
// 1.79 -> 1.92 ms/op, within noise) — the planner never picks the action
// index for a multi-value prefix range, because (action, at) cannot yield a
// global at-DESC stream. TestAuditActionIndex_PlannerChoice
// (audit_action_index_test.go) proves the split with EXPLAIN: prefix-range
// rides the at-ordered index; EXACT-action queries DO use the action index
// (that is what it earns its keep for). Both carry a cheap incremental sort
// from the id cursor tiebreaker + the cross-partition merge — inherent,
// not an index defect. Conclusion: no partial-per-action-class index is
// warranted; the prefix-range path is already index-driven and pushdown wins
// on bytes. BACKLOG entry closed.
func BenchmarkAuditListPushdown(b *testing.B) {
	ctx := context.Background()
	pool := startPostgres(b)
	seedAuditRows(b, ctx, pool, benchAuditRows)
	mustExec(b, ctx, pool, `ANALYZE audit_log`)

	repo := adapters.NewAuditRepoV2(sqlc.New(pool), pool)
	cutoff := benchAnchor.Add(-30 * 24 * time.Hour) // last 30 days

	b.Run("pushdown", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			out, _, err := repo.List(ctx, admindomain.ListAuditArgs{
				ActionPrefix: benchAuditMatchPrefix,
				AtGTE:        cutoff,
				PageSize:     50,
			})
			if err != nil {
				b.Fatal(err)
			}
			if len(out) == 0 {
				b.Fatal("pushdown returned no rows — bad fixture")
			}
		}
	})

	b.Run("no_pushdown", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			matched, scanned := 0, 0
			var afterAt time.Time
			var afterID uuid.UUID
			for matched < 50 {
				page, _, err := repo.List(ctx, admindomain.ListAuditArgs{
					PageSize: 1000,
					AfterAt:  afterAt,
					AfterID:  afterID,
				})
				if err != nil {
					b.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				for _, e := range page {
					scanned++
					if !e.At.Before(cutoff) && strings.HasPrefix(e.Action, benchAuditMatchPrefix) {
						matched++
					}
				}
				last := page[len(page)-1]
				afterAt, afterID = last.At, last.EntryID
			}
			if matched == 0 {
				b.Fatalf("no_pushdown matched nothing after scanning %d rows", scanned)
			}
		}
	})
}
