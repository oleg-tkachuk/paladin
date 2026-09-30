package userh

import (
	"testing"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// A filter compiles against the SCHEMA and evaluates against the PROJECTION.
// A schema field with no key in the projection produces an expression that
// compiles, runs, and matches nothing — every row, every request, no error.
// An empty page is a legitimate answer, so nobody can tell the difference.
// This ties the two halves together so the gap cannot open quietly.
func TestUserRowCoversEverySchemaField(t *testing.T) {
	row := userRow(authstore.User{Subject: "u1"})

	for _, field := range celpkg.UserSchema.Fields() {
		if _, ok := row[field]; !ok {
			t.Errorf("UserSchema declares %q and userRow does not supply it — a filter on that field silently matches nothing", field)
		}
	}
}

// The milder direction: a projected key the schema does not declare is dead
// weight no filter can reference, and usually means one side was updated alone.
func TestUserRowProjectsNothingUndeclared(t *testing.T) {
	row := userRow(authstore.User{Subject: "u1"})
	declared := map[string]bool{}
	for _, f := range celpkg.UserSchema.Fields() {
		declared[f] = true
	}

	for key := range row {
		if !declared[key] {
			t.Errorf("userRow projects %q, which UserSchema does not declare", key)
		}
	}
}
