package adapters

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres splits foreign-key failures into two SQLSTATEs, and a column
// declared ON DELETE RESTRICT raises the one that is easy to forget:
//
//	23503  foreign_key_violation
//	23001  restrict_violation
//
// hardDeleteWith checked only 23503, so DeleteTenant(force=true) against a
// tenant with users — which every tenant has, since bootstrap creates one —
// fell through to the generic branch and returned CodeInternal carrying the
// raw "violates RESTRICT setting of foreign key constraint" text. The caller
// saw an internal error where the truthful answer was "this tenant still owns
// data".
func TestRestrictViolationIsTreatedAsForeignKeyViolation(t *testing.T) {
	t.Parallel()

	// The classifier as hardDeleteWith applies it.
	isFKFailure := func(err error) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23001")
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "restrict_violation — what users_tenant_id_fkey raises",
			err:  &pgconn.PgError{Code: "23001", Message: "violates RESTRICT setting"},
			want: true,
		},
		{
			name: "foreign_key_violation",
			err:  &pgconn.PgError{Code: "23503", Message: "violates foreign key constraint"},
			want: true,
		},
		{
			name: "wrapped restrict_violation still classifies",
			err:  fmt.Errorf("hard-delete tenant: %w", &pgconn.PgError{Code: "23001"}),
			want: true,
		},
		{
			name: "unique_violation is a different failure",
			err:  &pgconn.PgError{Code: "23505"},
			want: false,
		},
		{
			name: "a non-Postgres error is not an FK failure",
			err:  errors.New("connection reset"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFKFailure(tc.err); got != tc.want {
				t.Errorf("classified %v as %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
