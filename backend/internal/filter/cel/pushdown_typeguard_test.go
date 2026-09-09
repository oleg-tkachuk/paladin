package cel

import (
	"testing"
	"time"
)

// ExtractPushdown parses the caller's expression — it does not type-check it.
// The comment on the parse error says so: a failure there is non-fatal because
// the same expression is compiled and evaluated authoritatively elsewhere. So
// a type-mismatched conjunct like `enabled == "yes"` arrives here intact, and
// the fieldType guards on each extractor are the only thing standing between
// it and a predicate in the SQL.
//
// That direction is the unsafe one. The contract is that a pushdown only ever
// NARROWS the candidate set — too wide costs a scan and the CEL pass fixes it,
// too narrow drops rows the filter accepts and nothing can put them back. A
// predicate comparing a bool column to a string does not merely narrow: it
// narrows on a comparison the database was never asked to make.
//
// Each guard is a disjunction — `!ok || fieldType(...) != typeX` — and as a
// conjunction the type half stops refusing.

func TestPushdown_TypeMismatchedConjunctsArePushedNowhere(t *testing.T) {
	cases := map[string]string{
		// A string literal against a bool column.
		"string equality on a bool field":   `enabled == "yes"`,
		"string inequality on a bool field": `enabled != "yes"`,
		"startsWith on a bool field":        `enabled.startsWith("y")`,
		"contains on a bool field":          `enabled.contains("y")`,
		// A string literal against a timestamp column.
		"string equality on a timestamp field": `created_at == "2026-01-01"`,
		"startsWith on a timestamp field":      `created_at.startsWith("2026")`,
		// A timestamp bound on a column that is not one.
		"timestamp bound on a string field": `backend_id >= timestamp("2026-01-01T00:00:00Z")`,
		"timestamp bound on a bool field":   `enabled < timestamp("2026-01-01T00:00:00Z")`,
		// Negation is the bool form, so it must refuse a non-bool field.
		"negation of a string field": `!backend_id`,
		// A field the schema does not carry at all.
		"unknown field": `nonexistent == "x"`,
	}

	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			pd, err := ExtractPushdown(StorageBackendSchema, expr)
			if err != nil {
				// A parse failure is an acceptable outcome — it also pushes
				// nothing. What must not happen is a predicate.
				return
			}
			assertNoPredicates(t, pd, expr)
		})
	}
}

// assertNoPredicates fails if any predicate map or bound carries an entry.
// Checking every field rather than the one under test means a conjunct that
// lands in the wrong bucket is caught too.
func assertNoPredicates(t *testing.T, pd Pushdown, expr string) {
	t.Helper()
	for field, v := range pd.Eq {
		t.Errorf("%s produced Eq[%q] = %q", expr, field, v)
	}
	for field, v := range pd.Neq {
		t.Errorf("%s produced Neq[%q] = %q", expr, field, v)
	}
	for field, v := range pd.BoolEq {
		t.Errorf("%s produced BoolEq[%q] = %v", expr, field, v)
	}
	for field, v := range pd.Prefix {
		t.Errorf("%s produced Prefix[%q] = %q", expr, field, v)
	}
	for field, v := range pd.Contains {
		t.Errorf("%s produced Contains[%q] = %q", expr, field, v)
	}
	for field, v := range pd.TimeGTE {
		t.Errorf("%s produced TimeGTE[%q] = %v", expr, field, v)
	}
	for field, v := range pd.TimeLTE {
		t.Errorf("%s produced TimeLTE[%q] = %v", expr, field, v)
	}
}

// The positive control: each shape the guards admit must still be pushed, so
// the refusals above are the type check doing its job rather than the
// extractor having stopped working.
func TestPushdown_WellTypedConjunctsStillPush(t *testing.T) {
	pd, err := ExtractPushdown(StorageBackendSchema,
		`backend_id == "primary" && provider != "aws" && enabled == true && `+
			`region.startsWith("eu") && endpoint.contains("internal") && `+
			`created_at >= timestamp("2026-01-01T00:00:00Z")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}

	if got := pd.Eq["backend_id"]; got != "primary" {
		t.Errorf("Eq[backend_id] = %q, want primary", got)
	}
	if got := pd.Neq["provider"]; got != "aws" {
		t.Errorf("Neq[provider] = %q, want aws", got)
	}
	if got, ok := pd.BoolEq["enabled"]; !ok || !got {
		t.Errorf("BoolEq[enabled] = (%v, %v), want (true, true)", got, ok)
	}
	if got := pd.Prefix["region"]; got != "eu" {
		t.Errorf("Prefix[region] = %q, want eu", got)
	}
	if got := pd.Contains["endpoint"]; got != "internal" {
		t.Errorf("Contains[endpoint] = %q, want internal", got)
	}
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, ok := pd.TimeGTE["created_at"]; !ok || !got.Equal(want) {
		t.Errorf("TimeGTE[created_at] = (%v, %v), want %v", got, ok, want)
	}
}

// A mismatched conjunct sitting beside well-typed ones must drop out on its
// own without taking them with it — the walk is over a top-level && chain, and
// each conjunct is judged separately.
func TestPushdown_AMismatchedConjunctDoesNotPoisonItsNeighbours(t *testing.T) {
	pd, err := ExtractPushdown(StorageBackendSchema,
		`backend_id == "primary" && enabled == "yes" && region.startsWith("eu")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	if got := pd.Eq["backend_id"]; got != "primary" {
		t.Errorf("Eq[backend_id] = %q, want primary", got)
	}
	if got := pd.Prefix["region"]; got != "eu" {
		t.Errorf("Prefix[region] = %q, want eu", got)
	}
	if v, ok := pd.Eq["enabled"]; ok {
		t.Errorf("the mismatched conjunct produced Eq[enabled] = %q", v)
	}
	if v, ok := pd.BoolEq["enabled"]; ok {
		t.Errorf("the mismatched conjunct produced BoolEq[enabled] = %v", v)
	}
}
