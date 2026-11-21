package adapters

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
)

// blockingResource names what a caller has to remove before a delete will
// succeed, in the caller's terms: the table Postgres reports is translated,
// and one with no name a caller knows — or none at all — is "other records".
// Neither the table nor the constraint is in the answer; the RPC log carries
// both.
func TestBlockingResource(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"a table with a noun": {
			err:  &pgconn.PgError{Code: "23001", TableName: "users", ConstraintName: "users_tenant_id_fkey"},
			want: "users",
		},
		"a table named for its rows": {
			err:  &pgconn.PgError{Code: "23503", TableName: "tenant_default_bindings", ConstraintName: "tenant_default_bindings_bucket_id_fkey"},
			want: "tenant default bindings",
		},
		"constraint only": {
			err:  &pgconn.PgError{Code: "23503", ConstraintName: "users_tenant_id_fkey"},
			want: "other records",
		},
		"a table with no noun": {
			err:  &pgconn.PgError{Code: "23503", TableName: "some_new_table"},
			want: "other records",
		},
		"wrapped still resolves": {
			err:  fmt.Errorf("delete tenant: %w", &pgconn.PgError{Code: "23503", TableName: "collections"}),
			want: "collections",
		},
		"not a postgres error": {
			err:  errors.New("connection refused"),
			want: "other records",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := blockingResource(tc.err); got != tc.want {
				t.Errorf("blockingResource = %q, want %q", got, tc.want)
			}
		})
	}
}

// A refusal built from a foreign-key violation reads as the resource holding
// the delete, with no table, constraint or SQLSTATE in it, and keeps the
// driver's error in its chain for the log and for classification.
func TestRestrictRefusal(t *testing.T) {
	pg := &pgconn.PgError{Severity: "ERROR", Code: "23001", Message: `update or delete on table "tenants" violates RESTRICT setting`, TableName: "users", ConstraintName: "users_tenant_id_fkey"}
	err := restrictRefusal(tenanth.ErrTenantHasChildren, fmt.Errorf("hard-delete: %w", pg))

	const want = "tenant still owns resources; remove them before purging it: held by users"
	if err.Error() != want {
		t.Errorf("message %q, want %q", err, want)
	}
	for _, leak := range []string{"users_tenant_id_fkey", "SQLSTATE", "RESTRICT"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("message %q carries %q", err, leak)
		}
	}
	if !errors.Is(err, tenanth.ErrTenantHasChildren) {
		t.Error("the sentinel is lost")
	}
	if got := pgerr.Table(err); got != pg.TableName {
		t.Errorf("the driver error is lost from the chain: table %q", got)
	}
}

// lockRefusal is how a purge refused by Object Lock reaches the caller as a
// precondition instead of an Internal error with raw SQL in it. Both lock
// triggers raise check_violation, and so does every ordinary CHECK, so the
// message is what tells them apart — a CHECK failing for another reason must
// not be reported as a legal hold.
func TestLockRefusal(t *testing.T) {
	const lockedVersion = "object version v"
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
			msg, got := lockRefusal(tc.err)
			if got != tc.want {
				t.Errorf("lockRefusal = %v, want %v", got, tc.want)
			}
			// The message the trigger raises is passed through on purpose:
			// the migrations word it for the caller, naming the version
			// held, and it names no table, trigger or constraint.
			if got && !strings.Contains(msg, lockedVersion) {
				t.Errorf("lockRefusal message %q does not name the version held", msg)
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
