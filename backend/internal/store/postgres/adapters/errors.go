package adapters

import "github.com/oleg-tkachuk/paladin/internal/store/postgres/pgerr"

// blockingRelation names the table whose rows are refusing a delete, for the
// message an operator reads.
//
// Postgres fills TableName on a foreign-key violation with the REFERENCING
// table, which is exactly the answer; the constraint name is the fallback,
// because a name an operator can grep for beats a guess. This used to derive
// the table by stripping suffixes off the constraint name — see pgerr.Table
// for why that cannot be made correct.
func blockingRelation(err error) string {
	if t := pgerr.Table(err); t != "" {
		return t
	}
	if c := pgerr.Constraint(err); c != "" {
		return c
	}
	return "another table"
}
