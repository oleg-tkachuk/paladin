package adapters

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
)

// blockingResource names what is refusing a delete, as the caller knows it.
//
// Postgres fills TableName on a foreign-key violation with the REFERENCING
// table, which is the answer; admindomain.RelationNoun translates it, since a
// caller has never seen a table. The table and the constraint stay in the
// error's chain (restrictRefusal), where the RPC log line finds them. This
// used to derive the table by stripping suffixes off the constraint name — see
// pgerr.Table for why that cannot be made correct.
func blockingResource(err error) string {
	return admindomain.RelationNoun(pgerr.Table(err))
}

// restrictRefusal is public refused by the foreign-key violation err: it reads
// as public and the resource holding the delete, and keeps err in its chain.
func restrictRefusal(public, err error) error {
	return pgerr.Withhold(fmt.Errorf("%w: held by %s", public, blockingResource(err)), err)
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
