package adapters

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// Every field ObjectSchema declares reaches the filter. The timestamps did
// not: `created_at > timestamp(...)` compiled and then failed on every row.
func TestObjectFilterSeesEveryField(t *testing.T) {
	committed := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	o := objecth.Object{
		Key: "logs/a.txt", State: "AVAILABLE", ContentType: "text/plain",
		CreatedAt: committed.Add(-time.Hour), UpdatedAt: committed, CommittedAt: &committed,
	}
	ev := cel.NewEvaluator()
	for _, expr := range []string{
		`created_at < timestamp("2026-05-01T00:00:00Z")`,
		`updated_at == timestamp("2026-05-01T00:00:00Z")`,
		`committed_at == timestamp("2026-05-01T00:00:00Z")`,
		`key.startsWith("logs/")`,
	} {
		prog, err := ev.Compile(cel.ObjectSchema, expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		ok, err := cel.Match(prog, celVars(o))
		if err != nil || !ok {
			t.Errorf("%s: match=%v err=%v; want a match", expr, ok, err)
		}
	}
}

// A row the expression cannot be evaluated on is a non-match, not a failed
// page: one untagged object used to turn a tag filter into an Internal error.
func TestRowMatchesTreatsMissingValuesAsNoMatch(t *testing.T) {
	ev := cel.NewEvaluator()
	prog, err := ev.Compile(cel.ObjectSchema, `tags["env"] == "prod"`)
	if err != nil {
		t.Fatal(err)
	}
	if rowMatches(prog, objecth.Object{Tags: map[string]string{"other": "x"}}) {
		t.Error("object without the tag matched")
	}
	if !rowMatches(prog, objecth.Object{Tags: map[string]string{"env": "prod"}}) {
		t.Error("tagged object did not match")
	}
	prog, _ = ev.Compile(cel.ObjectSchema, `committed_at > timestamp("2000-01-01T00:00:00Z")`)
	if rowMatches(prog, objecth.Object{}) {
		t.Error("uncommitted object matched a committed_at filter")
	}
}
