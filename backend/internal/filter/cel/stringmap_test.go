package cel

import (
	"testing"

	"cel.dev/cel-go/cel"
)

// stringMapSchema declares one string map, as TenantSchema's labels and
// ObjectSchema's tags are.
var stringMapSchema = &Schema{
	Name: "StringMapTest",
	vars: map[string]*cel.Type{"labels": stringMapType},
}

// A lookup of a key the map does not hold reads as the empty string, the way
// a NULL column reads to a filter, so the outcome of a filter never depends on
// which rows a page happens to hold. Presence is still asked of the map itself.
func TestStringMapLookupCannotFail(t *testing.T) {
	ev := NewEvaluator()
	held := map[string]string{"env": "prod"}
	empty := map[string]string{}
	for expr, want := range map[string][2]bool{ // [on held, on empty]
		`labels["env"] == "prod"`:                   {true, false},
		`labels.env == "prod"`:                      {true, false},
		`labels["env"] != "prod"`:                   {false, true},
		`labels["env"] == ""`:                       {false, true},
		`labels["env"].startsWith("pr")`:            {true, false},
		`has(labels.env)`:                           {true, false},
		`"env" in labels`:                           {true, false},
		`size(labels) == 1`:                         {true, false},
		`labels.exists(k, k == "env")`:              {true, false},
		`labels["env"] == "prod" || labels.x == ""`: {true, true},
	} {
		for i, labels := range []map[string]string{held, empty} {
			prog, err := ev.Compile(stringMapSchema, expr)
			if err != nil {
				t.Fatalf("%s: compile: %v", expr, err)
			}
			got, err := Match(prog, map[string]any{"labels": labels})
			if err != nil {
				t.Errorf("%s over %v: %v", expr, labels, err)
				continue
			}
			if got != want[i] {
				t.Errorf("%s over %v = %v, want %v", expr, labels, got, want[i])
			}
		}
	}
}

// A page that mixes rows with and without the key filters as each row
// alone would; it used to fail the whole page on the first row lacking it.
func TestFilterPageOverRowsWithoutTheKey(t *testing.T) {
	page := []map[string]string{{"env": "prod"}, {}, {"team": "core"}, {"env": "prod"}}
	kept, err := FilterPage(NewEvaluator(), stringMapSchema, `labels.env == "prod"`, page,
		func(l map[string]string) map[string]any { return map[string]any{"labels": l} })
	if err != nil {
		t.Fatalf("FilterPage: %v", err)
	}
	if len(kept) != 2 {
		t.Errorf("kept %v, want the two labelled prod", kept)
	}
}
