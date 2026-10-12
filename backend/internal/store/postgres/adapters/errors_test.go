package adapters

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// blockingRelation names the table an operator has to go and empty before a
// delete will succeed. The precedence is the whole content of the function:
// the table name is the answer, the constraint name is a grep-able fallback,
// and the generic phrase is what is left when Postgres told us neither.
func TestBlockingRelation(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"table wins over constraint": {
			err:  &pgconn.PgError{Code: "23503", TableName: "users", ConstraintName: "users_tenant_id_fkey"},
			want: "users",
		},
		"constraint when no table": {
			err:  &pgconn.PgError{Code: "23503", ConstraintName: "users_tenant_id_fkey"},
			want: "users_tenant_id_fkey",
		},
		"neither": {
			err:  &pgconn.PgError{Code: "23503"},
			want: "another table",
		},
		"wrapped still resolves": {
			err:  fmt.Errorf("delete tenant: %w", &pgconn.PgError{Code: "23503", TableName: "objects"}),
			want: "objects",
		},
		"not a postgres error": {
			err:  errors.New("connection refused"),
			want: "another table",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := blockingRelation(tc.err); got != tc.want {
				t.Errorf("blockingRelation = %q, want %q", got, tc.want)
			}
		})
	}
}

// lockRefusal is how a purge refused by Object Lock reaches the caller as a
// precondition instead of an Internal error with raw SQL in it. Both lock
// triggers raise check_violation, and so does every ordinary CHECK, so the
// message is what tells them apart — a CHECK failing for another reason must
// not be reported as a legal hold.
func TestLockRefusal(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"tenant purge guard (061)": {
			err:  &pgconn.PgError{Code: "23514", Message: "tenant t holds object version v under legal hold or retention"},
			want: true,
		},
		"retention trigger, legal hold": {
			err:  &pgconn.PgError{Code: "23514", Message: "object version v is under legal hold"},
			want: true,
		},
		"retention trigger, COMPLIANCE": {
			err:  &pgconn.PgError{Code: "23514", Message: "object version v is retained under COMPLIANCE until 2027-01-01"},
			want: true,
		},
		"wrapped still resolves": {
			err:  fmt.Errorf("hard-delete tenant: %w", &pgconn.PgError{Code: "23514", Message: "object version v is under legal hold"}),
			want: true,
		},
		"an ordinary CHECK is not a lock": {
			err:  &pgconn.PgError{Code: "23514", Message: `new row violates check constraint "tenants_slug_format"`},
			want: false,
		},
		"the right words under another code are not a lock": {
			err:  &pgconn.PgError{Code: "23503", Message: "under legal hold"},
			want: false,
		},
		"not a postgres error": {
			err:  errors.New("under legal hold"),
			want: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, got := lockRefusal(tc.err); got != tc.want {
				t.Errorf("lockRefusal = %v, want %v", got, tc.want)
			}
		})
	}
}

// The quota upsert RETURNINGs no row when the version guard fails, so
// ErrNoRows here is a concurrent writer rather than a missing row — reporting
// it as anything else would tell the caller to retry a request that will keep
// failing until they re-read the version.
func TestMapQuotaOCCErr(t *testing.T) {
	if got := mapQuotaOCCErr(pgx.ErrNoRows); !errors.Is(got, admindomain.ErrVersionMismatch) {
		t.Errorf("ErrNoRows mapped to %v, want ErrVersionMismatch", got)
	}
	if got := mapQuotaOCCErr(fmt.Errorf("upsert: %w", pgx.ErrNoRows)); !errors.Is(got, admindomain.ErrVersionMismatch) {
		t.Errorf("wrapped ErrNoRows mapped to %v, want ErrVersionMismatch", got)
	}

	other := errors.New("connection refused")
	if got := mapQuotaOCCErr(other); !errors.Is(got, other) {
		t.Errorf("unrelated error mapped to %v, want it passed through", got)
	}
	if got := mapQuotaOCCErr(nil); got != nil {
		t.Errorf("nil mapped to %v, want nil", got)
	}
}
