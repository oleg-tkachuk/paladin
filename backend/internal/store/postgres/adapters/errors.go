package adapters

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
)

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

// lockRefusal reports whether err is an Object Lock trigger refusing a
// delete — tenants_purge_lock_guard (061) or object_locks_enforce_retention —
// and returns the trigger's message, which names the version holding it.
//
// Both raise check_violation, which an ordinary CHECK raises too, so the
// message is part of the test: a constraint failure from another cause must
// not be reported as a lock.
func lockRefusal(err error) (string, bool) {
	if !pgerr.Is(err, pgerr.CheckViolation) {
		return "", false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	if strings.Contains(pgErr.Message, "legal hold") ||
		strings.Contains(pgErr.Message, "retained under") {
		return pgErr.Message, true
	}
	return "", false
}
