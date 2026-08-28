package objectkey

import (
	"testing"

	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// A filter compiles against the SCHEMA and evaluates against the PROJECTION.
// A schema field with no key in the projection produces an expression that
// compiles, runs, and matches nothing — every row, every request, no error.
// An empty page is a legitimate answer, so nobody can tell the difference.
// This ties the two halves together so the gap cannot open quietly.
func TestCollectionRowCoversEverySchemaField(t *testing.T) {
	row := collectionRow(Collection{Collection: "docs"})

	for _, field := range celpkg.CollectionSchema.Fields() {
		if _, ok := row[field]; !ok {
			t.Errorf("CollectionSchema declares %q and collectionRow does not supply it — a filter on that field silently matches nothing", field)
		}
	}
}

// The milder direction: a projected key the schema does not declare is dead
// weight no filter can reference, and usually means one side was updated alone.
func TestCollectionRowProjectsNothingUndeclared(t *testing.T) {
	row := collectionRow(Collection{Collection: "docs"})
	declared := map[string]bool{}
	for _, f := range celpkg.CollectionSchema.Fields() {
		declared[f] = true
	}

	for key := range row {
		if !declared[key] {
			t.Errorf("collectionRow projects %q, which CollectionSchema does not declare", key)
		}
	}
}
