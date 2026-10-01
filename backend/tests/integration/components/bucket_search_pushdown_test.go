//go:build integration

// The `search` filter field is defined twice — once in Go (cel.SearchText, for
// the authoritative CEL pass) and once in SQL (ListBucketsV2's `search_like`
// clause, for the pushdown). Two spellings of one definition.
//
// The pushdown contract is that a hint only NARROWS: the rows SQL returns must
// include every row the CEL program accepts. If the two spellings drift, SQL
// drops a row CEL would have kept, and the operator is told a bucket does not
// exist. Nothing about that failure is visible — the query succeeds, the list
// renders, the bucket is simply absent.
//
// So this asserts the contract directly rather than the SQL: filter through the
// repo (SQL narrowing only), filter the same rows in Go (CEL only), and require
// the two to agree. A case-folding difference between Go and Postgres, a
// separator that does not match, a LIKE metacharacter reaching the query — each
// shows up here as a set difference, named.
package components

import (
	"context"
	"fmt"

	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

func subtestBucketSearch(ctx context.Context, pool *pgxpool.Pool) func(*testing.T) {
	return func(t *testing.T) {
		const backendID = "search-backend"
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)
		repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

		// Chosen to break the definition rather than to exercise it.
		rows := []struct{ name, display string }{
			{"prod-logs", "Prod Logs"},       // plain ASCII, mixed case
			{"staging-logs", "STAGING LOGS"}, // all caps
			{"quiet", ""},                    // NULL-ish display name
			{"istanbul-tr", "İstanbul"},      // Go and Postgres fold this differently
			{"uber-cache", "Über Cache"},     // non-ASCII, foldable in Unicode only
			{"pct-bucket", "100% done"},      // LIKE metacharacter in the DATA
			{"under-score", "a_b"},           // the other LIKE metacharacter
			{"ab", "cd"},                     // for the separator-spanning probe
		}
		for _, r := range rows {
			if err := repo.Create(ctx, admindomain.Bucket{
				BackendID: backendID, BucketName: r.name, DisplayName: r.display,
			}); err != nil {
				t.Fatalf("create %s: %v", r.name, err)
			}
		}

		all, _, err := repo.List(ctx, admindomain.ListBucketsArgs{
			BackendID: backendID, PageSize: 1000,
		})
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if len(all) != len(rows) {
			t.Fatalf("fixture: listed %d buckets, created %d", len(all), len(rows))
		}

		queries := []string{
			"prod",     // lowercase query, mixed-case data
			"PROD",     // uppercase query, lowercase data
			"Logs",     // matches display name only
			"quiet",    // matches bucket id only
			"istanbul", // ASCII query over data Postgres and Go fold differently
			"İstanbul", // non-ASCII query
			"über",     // non-ASCII, lowercase
			"ÜBER",     // non-ASCII, uppercase — ASCII folding does NOT match this
			"100%",     // LIKE wildcard as data
			"a_b",      // LIKE single-char wildcard as data
			"b\nc",     // spans the separator: must match nothing
			"",         // no filter at all
			"nosuchthing",
		}

		for _, q := range queries {
			t.Run(q, func(t *testing.T) {
				filter := ""
				if q != "" {
					filter = "search.contains(" + celQuote(q) + ")"
				}

				// SQL narrowing only.
				narrowed, _, err := repo.List(ctx, admindomain.ListBucketsArgs{
					BackendID: backendID, PageSize: 1000, Filter: filter,
				})
				if err != nil {
					t.Fatalf("list filtered: %v", err)
				}

				// The authoritative pass, over each side.
				viaSQL := celFilterBuckets(t, filter, narrowed)
				viaAll := celFilterBuckets(t, filter, all)

				if !sameBuckets(viaSQL, viaAll) {
					t.Errorf("pushdown changed the answer for %q\n"+
						"  after SQL narrowing: %v\n"+
						"  over every row:      %v\n"+
						"the SQL in ListBucketsV2 and cel.SearchText disagree — SQL "+
						"dropped a row the filter accepts, which an operator sees as "+
						"a bucket that does not exist",
						q, bucketNames(viaSQL), bucketNames(viaAll))
				}
			})
		}
	}
}

func celFilterBuckets(t *testing.T, filter string, in []admindomain.Bucket) []admindomain.Bucket {
	t.Helper()
	if filter == "" {
		return in
	}
	// A COPY, because FilterPage compacts in place (`out := page[:0]`) and this
	// helper is called twice per subtest over a slice shared by all of them.
	// The first version did not copy: the opening subtest overwrote the shared
	// fixture, and later ones reported the same bucket twice and a match that
	// had vanished. Two failures that looked like the drift this file hunts,
	// produced entirely by the test.
	in = append([]admindomain.Bucket(nil), in...)
	out, err := celpkg.FilterPage(celpkg.NewEvaluator(), celpkg.PhysicalBucketSchema, filter, in,
		func(b admindomain.Bucket) map[string]any {
			return map[string]any{
				"bucket_id":    b.BucketName,
				"display_name": b.DisplayName,
				"search":       celpkg.SearchText(b.BucketName, b.DisplayName, b.BackendID),
			}
		})
	if err != nil {
		t.Fatalf("cel filter %q: %v", filter, err)
	}
	return out
}

func sameBuckets(a, b []admindomain.Bucket) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x.BucketName] = true
	}
	for _, x := range b {
		if !seen[x.BucketName] {
			return false
		}
	}
	return true
}

func bucketNames(in []admindomain.Bucket) []string {
	out := make([]string, 0, len(in))
	for _, b := range in {
		out = append(out, b.BucketName)
	}
	return out
}

// celQuote renders a Go string as a CEL string literal.
func celQuote(s string) string {
	out := []rune(`"`)
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, r)
		}
	}
	return string(append(out, '"'))
}

// The bug the `search` field exists to end: a match that sorts past the page.
//
// This is the shape every other TestPushdown_* case in list_filter_pushdown_test.go
// asserts, and buckets had no such case. It is also the one the console change
// depends on — a picker that sends a filter and reads one page is correct only
// if SQL, not the CEL pass, did the narrowing.
func subtestBucketPastThePage(ctx context.Context, pool *pgxpool.Pool) func(*testing.T) {
	return func(t *testing.T) {
		const backendID = "search-page-backend"
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)
		repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

		// 60 rows, page size 10. The needle sorts last on purpose.
		for i := 0; i < 59; i++ {
			if err := repo.Create(ctx, admindomain.Bucket{
				BackendID: backendID, BucketName: fmt.Sprintf("aaa-%03d", i),
			}); err != nil {
				t.Fatalf("create filler %d: %v", i, err)
			}
		}
		if err := repo.Create(ctx, admindomain.Bucket{
			BackendID: backendID, BucketName: "zzz-needle", DisplayName: "The Needle",
		}); err != nil {
			t.Fatalf("create needle: %v", err)
		}

		got, _, err := repo.List(ctx, admindomain.ListBucketsArgs{
			BackendID: backendID, PageSize: 10,
			Filter: `search.contains("needle")`,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].BucketName != "zzz-needle" {
			t.Errorf("first page of a filtered list = %v, want [zzz-needle] — the "+
				"filter did not reach SQL, so it selected from the page instead of "+
				"from the table and the operator is told the bucket does not exist",
				bucketNames(got))
		}
	}
}
