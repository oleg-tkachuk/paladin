package adapters

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
)

// SQLSTATEs the binding trigger and an unrelated constraint raise.
const (
	sqlstateCheckViolation  = "23514"
	sqlstateUniqueViolation = "23505"
)

// The binding trigger's refusal is ErrBucketNotGranted naming the bucket, and
// nothing of the trigger's own error, which the caller is sent; any other
// failure is not a tenancy refusal.
func TestTenancyError(t *testing.T) {
	refusal := &pgconn.PgError{Severity: "ERROR", Code: sqlstateCheckViolation, ConstraintName: schema.CollectionsBucketTenancy, Message: "tenant may not bind bucket 7"}
	got := tenancyError(refusal, "be-1", "bk-1")
	if !errors.Is(got, admindomain.ErrBucketNotGranted) || !strings.Contains(got.Error(), "be-1/bk-1") {
		t.Fatalf("refusal = %v, want ErrBucketNotGranted naming be-1/bk-1", got)
	}
	if pgerr.Constraint(got) != "" || strings.Contains(got.Error(), refusal.Message) {
		t.Errorf("refusal %q carries the trigger's error", got)
	}

	for name, err := range map[string]error{
		"another check":  &pgconn.PgError{Code: sqlstateCheckViolation, ConstraintName: schema.CollectionsBucketVisibility},
		"another code":   &pgconn.PgError{Code: sqlstateUniqueViolation, ConstraintName: schema.CollectionsBucketTenancy},
		"not a pg error": errors.New("conn closed"),
	} {
		if got := tenancyError(err, "be-1", "bk-1"); got != nil {
			t.Errorf("%s: %v, want nil", name, got)
		}
	}
}
