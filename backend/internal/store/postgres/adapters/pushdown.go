package adapters

import (
	"bytes"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// hints extracts the SQL-expressible subset of a caller's CEL filter.
//
// Every List query in this package that accepts a `filter` used to hand the
// whole page to an in-memory CEL pass, which meant `filter` narrowed whatever
// the page happened to contain rather than the table: with 559 buckets and a
// 500-row page, a filter matching the 559th returned an empty first page, and
// a client that stops on an empty page concluded there was no match. The
// hints go into the query so the predicate selects from the table.
//
// A parse failure is deliberately swallowed: the handler compiles the same
// expression and reports the error authoritatively, and a zero Pushdown means
// "narrow nothing", which is always correct — just slower.
func hints(schema *cel.Schema, filter string) cel.Pushdown {
	if filter == "" {
		return cel.Pushdown{}
	}
	pd, err := cel.ExtractPushdown(schema, filter)
	if err != nil {
		return cel.Pushdown{}
	}
	return pd
}

// createdBounds converts a pushdown's created_at range into the pgtype pair
// every List query now takes. A nil bound becomes the zero Timestamptz, which
// the query reads as SQL NULL and therefore as "unbounded on that side".
//
// Only created_at is threaded. Every filterable schema exposes it, it is the
// bound callers actually write ("everything since the incident"), and it is
// immutable — an updated_at range pushed into the query could exclude a row
// that the authoritative CEL pass, running microseconds later against a row
// someone just touched, would have accepted.
func createdBounds(pd cel.Pushdown) (gte pgtype.Timestamptz, lte pgtype.Timestamptz) {
	lo, hi := pd.TimeHint("created_at")
	if lo != nil {
		gte = pgTS(*lo)
	}
	if hi != nil {
		lte = pgTS(*hi)
	}
	return gte, lte
}

// branchHints is hints for a list that can run one query per disjunct: the
// filter's pushdown branches (cel.ExtractPushdownBranches), at least one. A
// parse failure narrows nothing, as in hints.
func branchHints(schema *cel.Schema, filter string) []cel.Pushdown {
	branches, err := cel.ExtractPushdownBranches(schema, filter)
	if err != nil || len(branches) == 0 {
		return []cel.Pushdown{{}}
	}
	return branches
}

// fanOut runs a list's query once per branch, each from the same cursor and
// with the same page size, and merges the pages (mergeBranchPages). One
// branch is the plain query, its page returned as it came.
//
// The branches run as separate statements, not in one snapshot: a row that
// changes between them can only appear in one more branch's page or one
// fewer, and the merge drops the repeat, so the cost is a row the handler's
// CEL pass judges on a slightly later version — never a row lost.
func fanOut[T any, K comparable](
	branches []cel.Pushdown, pageSize int32,
	fetch func(cel.Pushdown) ([]T, error), less func(a, b T) bool, key func(T) K,
) ([]T, error) {
	pages := make([][]T, 0, len(branches))
	for _, b := range branches {
		page, err := fetch(b)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return mergeBranchPages(pages, int(pageSize), less, key), nil
}

// mergeBranchPages merges per-branch keyset pages, each in the list's order
// and at most pageSize long, into one: the union in that order, each row once,
// cut to pageSize. less must be the list's full ORDER BY, a total order.
//
// The cut is what keeps the walk from skipping rows: a branch that filled its
// page may hold more after its last row, but all its pageSize rows are in the
// union, so the union's first pageSize rows never pass that last row. A list
// whose page comes back full therefore pages on from its last row, as it does
// with one query.
func mergeBranchPages[T any, K comparable](pages [][]T, pageSize int, less func(a, b T) bool, key func(T) K) []T {
	if len(pages) == 1 {
		return pages[0]
	}
	seen := map[K]bool{}
	var rows []T
	for _, page := range pages {
		for _, row := range page {
			if k := key(row); !seen[k] {
				seen[k] = true
				rows = append(rows, row)
			}
		}
	}
	slices.SortStableFunc(rows, func(a, b T) int {
		switch {
		case less(a, b):
			return -1
		case less(b, a):
			return 1
		default:
			return 0
		}
	})
	if len(rows) > pageSize {
		rows = rows[:pageSize]
	}
	return rows
}

// uuidBefore orders two uuids as Postgres does, byte by byte, so a merge in
// Go agrees with an ORDER BY on the column.
func uuidBefore(a, b pgtype.UUID) bool {
	return bytes.Compare(a.Bytes[:], b.Bytes[:]) < 0
}
