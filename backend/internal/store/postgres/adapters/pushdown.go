package adapters

import (
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
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
