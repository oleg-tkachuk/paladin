package pgerr

import (
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// goingAway are the operator-intervention codes that mean the server is
// stopping or not yet accepting. The rest of their class is not about
// reachability: 57014 is a statement cancelled by its own timeout.
var goingAway = map[string]bool{
	pgerrcode.AdminShutdown:    true,
	pgerrcode.CrashShutdown:    true,
	pgerrcode.CannotConnectNow: true,
}

// IsUnavailable reports whether err says the database could not be reached
// or is going away — a refused or timed-out connection, a connection
// exception, a server shutting down or still starting — rather than refusing
// what was asked of it. Such a failure passes on its own and is worth a retry.
func IsUnavailable(err error) bool {
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	return pgerrcode.IsConnectionException(pgErr.Code) || goingAway[pgErr.Code]
}
