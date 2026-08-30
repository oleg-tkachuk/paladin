package pgerr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func pgError(code, constraint string) error {
	return &pgconn.PgError{Code: code, ConstraintName: constraint}
}

// The pair that started this: a constraint declared ON DELETE RESTRICT raises
// 23001, not 23503, and adapters that checked only the latter reported a
// working refusal as an Internal error.
func TestClassify_RestrictAndForeignKeyAreOneKind(t *testing.T) {
	for _, code := range []string{"23503", "23001"} {
		if got := Classify(pgError(code, "")); got != ForeignKeyViolation {
			t.Errorf("Classify(%s) = %v, want ForeignKeyViolation", code, got)
		}
	}
}

func TestClassify_KnownCodes(t *testing.T) {
	for code, want := range map[string]Kind{
		"23505": UniqueViolation,
		"23514": CheckViolation,
		"23502": NotNullViolation,
		"40001": SerializationFailure,
		"40P01": SerializationFailure,
	} {
		if got := Classify(pgError(code, "")); got != want {
			t.Errorf("Classify(%s) = %v, want %v", code, got, want)
		}
	}
}

// Anything unrecognised must stay Unknown so callers treat it as a fault
// rather than quietly folding it into a business outcome.
func TestClassify_UnknownForOtherErrors(t *testing.T) {
	for name, err := range map[string]error{
		"plain error":     errors.New("boom"),
		"nil":             nil,
		"unmapped code":   pgError("22P02", ""),
		"different class": pgError("42P01", ""),
	} {
		if got := Classify(err); got != Unknown {
			t.Errorf("Classify(%s) = %v, want Unknown", name, got)
		}
	}
}

// Adapters wrap before returning; classification has to survive that.
func TestClassify_UnwrapsWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("adapters: delete tenant: %w",
		fmt.Errorf("pool: %w", pgError("23001", "collections_tenant_id_fkey")))
	if got := Classify(wrapped); got != ForeignKeyViolation {
		t.Fatalf("Classify(wrapped) = %v, want ForeignKeyViolation", got)
	}
	if got := Constraint(wrapped); got != "collections_tenant_id_fkey" {
		t.Errorf("Constraint(wrapped) = %q", got)
	}
}

// Is(err, Unknown) must never be true: "I could not classify this" is not a
// condition to branch on, and a caller writing it has a bug.
func TestIs_UnknownIsNeverTrue(t *testing.T) {
	if Is(errors.New("boom"), Unknown) {
		t.Error("Is(err, Unknown) = true")
	}
	if Is(pgError("23505", ""), Unknown) {
		t.Error("Is(pgError, Unknown) = true")
	}
}

// Two unique indexes on one table need different messages, so the constraint
// name has to come through.
func TestConstraint(t *testing.T) {
	if got := Constraint(pgError("23505", "tenants_slug_live_key")); got != "tenants_slug_live_key" {
		t.Errorf("Constraint = %q", got)
	}
	if got := Constraint(errors.New("boom")); got != "" {
		t.Errorf("Constraint(non-pg) = %q, want empty", got)
	}
	if !ConstraintIs(pgError("23505", "tenants_slug_live_key"), "tenants_slug_live_key") {
		t.Error("ConstraintIs = false for a matching name")
	}
}

// Table is what lets an adapter say which relation is refusing a delete. The
// constraint name cannot be parsed into it — see the doc comment — so the
// value has to come from Postgres, and this pins that it is read from the
// field Postgres fills rather than derived.
func TestTable(t *testing.T) {
	err := &pgconn.PgError{
		Code:           "23503",
		ConstraintName: "users_tenant_id_fkey",
		TableName:      "users",
	}
	if got := Table(err); got != "users" {
		t.Errorf("Table = %q, want %q", got, "users")
	}
	if got := Table(fmt.Errorf("hard-delete tenant: %w", err)); got != "users" {
		t.Errorf("wrapped: Table = %q, want %q", got, "users")
	}
	if got := Table(errors.New("not a pg error")); got != "" {
		t.Errorf("non-pg error: Table = %q, want empty", got)
	}
	if got := Table(nil); got != "" {
		t.Errorf("nil: Table = %q, want empty", got)
	}
}
