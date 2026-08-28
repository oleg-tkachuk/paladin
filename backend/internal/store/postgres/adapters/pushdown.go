package adapters

import (
	"github.com/jackc/pgx/v5/pgtype"

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
