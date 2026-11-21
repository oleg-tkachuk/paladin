package pgerr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The caller reads the public text alone; both errors stay findable, so the
// sentinel still decides the code and the driver's error still reaches the
// log.
func TestWithhold(t *testing.T) {
	public := errors.New("tenant still owns resources")
	cause := &pgconn.PgError{Code: "23001", TableName: "users", ConstraintName: "users_tenant_id_fkey"}
	err := Withhold(public, cause)

	if err.Error() != public.Error() {
		t.Errorf("text %q, want %q", err, public)
	}
	if !errors.Is(err, public) {
		t.Error("the public error is no longer found")
	}
	if got := Table(err); got != cause.TableName {
		t.Errorf("Table = %q, want the cause's %q", got, cause.TableName)
	}
	if !Is(err, ForeignKeyViolation) {
		t.Error("the cause no longer classifies")
	}
}

// A log line written with zap.Error carries %+v as errorVerbose: there the
// operator reads the table and the constraint the caller was not told.
func TestWithholdVerbose(t *testing.T) {
	public := errors.New("tenant still owns resources")
	cause := &pgconn.PgError{Severity: "ERROR", Code: "23001", Message: "violates RESTRICT setting", TableName: "users", ConstraintName: "users_tenant_id_fkey"}
	err := Withhold(public, cause)

	const want = "tenant still owns resources; withheld: ERROR: violates RESTRICT setting (SQLSTATE 23001) table=users constraint=users_tenant_id_fkey"
	if got := fmt.Sprintf("%+v", err); got != want {
		t.Errorf("%%+v = %q, want %q", got, want)
	}
	if got := fmt.Sprintf("%v", err); got != public.Error() {
		t.Errorf("%%v = %q, want the public text alone", got)
	}
}
