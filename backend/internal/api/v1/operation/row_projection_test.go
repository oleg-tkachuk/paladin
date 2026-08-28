package operation

import (
	"testing"

	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// A filter compiles against the SCHEMA and evaluates against the PROJECTION.
// A schema field with no key in the projection produces an expression that
// compiles, runs, and matches nothing — every row, every request, no error.
// An empty page is a legitimate answer, so nobody can tell the difference.
// This ties the two halves together so the gap cannot open quietly.
func TestOperationRowCoversEverySchemaField(t *testing.T) {
	row := operationRow(Operation{Type: "BatchCopy"})

	for _, field := range celpkg.OperationSchema.Fields() {
		if _, ok := row[field]; !ok {
			t.Errorf("OperationSchema declares %q and operationRow does not supply it — a filter on that field silently matches nothing", field)
		}
	}
}

// The milder direction: a projected key the schema does not declare is dead
// weight no filter can reference, and usually means one side was updated alone.
func TestOperationRowProjectsNothingUndeclared(t *testing.T) {
	row := operationRow(Operation{Type: "BatchCopy"})
	declared := map[string]bool{}
	for _, f := range celpkg.OperationSchema.Fields() {
		declared[f] = true
	}

	for key := range row {
		if !declared[key] {
			t.Errorf("operationRow projects %q, which OperationSchema does not declare", key)
		}
	}
}
