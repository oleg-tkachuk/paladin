package pgerr

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// unreachableDSN names a port nothing listens on, so a connection attempt
// fails at once with the driver's own ConnectError.
const unreachableDSN = "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"

// connectAttemptTimeout bounds the one real connection attempt the test makes.
const connectAttemptTimeout = 2 * time.Second

// refusedConnect is the error pgconn returns for a server it cannot reach.
func refusedConnect(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), connectAttemptTimeout)
	defer cancel()
	conn, err := pgconn.Connect(ctx, unreachableDSN)
	if err == nil {
		_ = conn.Close(ctx)
		t.Fatal("a connection to a closed port succeeded")
	}
	return err
}

func TestIsUnavailable(t *testing.T) {
	refused := refusedConnect(t)
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"refused connection", refused, true},
		{"refused connection, wrapped", fmt.Errorf("users: get: %w", refused), true},
		{"refused connection under a connect error", connect.NewError(connect.CodeInternal, refused.Error()).WithCause(refused), true},
		{"server shutting down", &pgconn.PgError{Code: pgerrcode.AdminShutdown}, true},
		{"server crashed", &pgconn.PgError{Code: pgerrcode.CrashShutdown}, true},
		{"server not accepting yet", &pgconn.PgError{Code: pgerrcode.CannotConnectNow}, true},
		{"connection failure", &pgconn.PgError{Code: pgerrcode.ConnectionFailure}, true},
		{"statement timeout", &pgconn.PgError{Code: pgerrcode.QueryCanceled}, false},
		{"unique violation", &pgconn.PgError{Code: pgerrcode.UniqueViolation}, false},
		{"not a database error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnavailable(tc.err); got != tc.want {
				t.Errorf("IsUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
