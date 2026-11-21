package middleware

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// withheldDriver reads as its public text and wraps a driver error, as a
// store's refusal built from a foreign-key violation does.
type withheldDriver struct {
	public string
	cause  error
}

func (w withheldDriver) Error() string   { return w.public }
func (w withheldDriver) Unwrap() []error { return []error{w.cause} }

// A refusal whose text leaves out the driver's error it was built from still
// logs that error: the caller is told which resource holds the delete, the
// operator reads the table and constraint.
func TestFailureLogsTheDriverErrorTheMessageLeftOut(t *testing.T) {
	const public = "tenant still owns resources: held by users"
	pg := &pgconn.PgError{Severity: "ERROR", Code: "23001", Message: "violates RESTRICT setting", TableName: "users", ConstraintName: "users_tenant_id_fkey"}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"withheld": {
			connect.NewError(connect.CodeFailedPrecondition, public).WithCause(withheldDriver{public, pg}),
			"ERROR: violates RESTRICT setting (SQLSTATE 23001) table=users constraint=users_tenant_id_fkey",
		},
		// How a store's refusal reaches the log: a sentinel's wrap mapped by
		// MapError, whose cause reads exactly as its message.
		"through MapError": {
			apiutil.MapError(withheldDriver{public, errors.Join(apiutil.ErrFailedPrecondition, pg)}),
			"ERROR: violates RESTRICT setting (SQLSTATE 23001) table=users constraint=users_tenant_id_fkey",
		},
		"already told": {connect.NewError(connect.CodeFailedPrecondition, pg.Error()).WithCause(withheldDriver{pg.Error(), pg}), ""},
	} {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			logFailure(zap.New(core), "/pkg.Svc/M", "req-1", time.Millisecond, tc.err)
			if got, _ := logs.All()[0].ContextMap()[fieldCause].(string); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}
