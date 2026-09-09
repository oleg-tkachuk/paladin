package cel

import (
	"testing"
	"time"
)

// The audit and object extractors are narrower than the generic one: each
// predicate is bound to one named column. `action.startsWith(…)` becomes an
// action prefix, `key.contains(…)` a key match, `at >= …` a time bound — and
// the field-name comparison is the only thing keeping a conjunct about some
// other column out of that slot.
//
// Getting it wrong is the narrowing direction, which is the one that loses
// rows: a filter on actor_subject recorded as an action prefix makes the
// query select on `action LIKE …` instead, and the rows the caller actually
// asked for never reach the authoritative CEL pass to be let back in. Each of
// those guards is a disjunction, and every one of them survived.

func TestAuditPushdown_PredicatesAreBoundToTheirOwnField(t *testing.T) {
	// Shapes that are well-formed CEL against fields the extractor does not
	// pull. None may land in a slot belonging to another column.
	for name, expr := range map[string]string{
		"startsWith on another field":  `actor_subject.startsWith("admin")`,
		"equality on another field":    `actor_subject == "admin@local"`,
		"lower bound on another field": `created_at >= timestamp("2026-01-01T00:00:00Z")`,
		"upper bound on another field": `created_at <= timestamp("2026-01-01T00:00:00Z")`,
		"contains on the action field": `action.contains("Bucket")`, // only startsWith is pulled
	} {
		t.Run(name, func(t *testing.T) {
			pd, err := ExtractAuditPushdown(expr)
			if err != nil {
				return // a parse failure also pushes nothing
			}
			if pd.ActionEq != "" {
				t.Errorf("%s produced ActionEq = %q", expr, pd.ActionEq)
			}
			if pd.ActionPrefix != "" {
				t.Errorf("%s produced ActionPrefix = %q", expr, pd.ActionPrefix)
			}
			if !pd.AtGTE.IsZero() {
				t.Errorf("%s produced AtGTE = %v", expr, pd.AtGTE)
			}
			if !pd.AtLTE.IsZero() {
				t.Errorf("%s produced AtLTE = %v", expr, pd.AtLTE)
			}
		})
	}

	// Positive control, so the refusals above are the field guard rather than
	// the extractor being broken.
	pd, err := ExtractAuditPushdown(
		`action.startsWith("admin.Bucket") && at >= timestamp("2026-01-01T00:00:00Z") && ` +
			`at <= timestamp("2026-02-01T00:00:00Z")`)
	if err != nil {
		t.Fatalf("ExtractAuditPushdown: %v", err)
	}
	if pd.ActionPrefix != "admin.Bucket" {
		t.Errorf("ActionPrefix = %q, want admin.Bucket", pd.ActionPrefix)
	}
	if want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !pd.AtGTE.Equal(want) {
		t.Errorf("AtGTE = %v, want %v", pd.AtGTE, want)
	}
	if want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC); !pd.AtLTE.Equal(want) {
		t.Errorf("AtLTE = %v, want %v", pd.AtLTE, want)
	}
}

func TestObjectPushdown_PredicatesAreBoundToTheirOwnField(t *testing.T) {
	for name, expr := range map[string]string{
		"equality on another field":   `content_type == "text/plain"`,
		"startsWith on another field": `content_type.startsWith("text/")`,
		"contains on another field":   `content_type.contains("plain")`,
		"startsWith on state":         `state.startsWith("AVAIL")`, // state takes equality only
	} {
		t.Run(name, func(t *testing.T) {
			pd, err := ExtractObjectPushdown(expr)
			if err != nil {
				return
			}
			if pd.StateEq != "" {
				t.Errorf("%s produced StateEq = %q", expr, pd.StateEq)
			}
			if pd.KeyPrefix != "" {
				t.Errorf("%s produced KeyPrefix = %q", expr, pd.KeyPrefix)
			}
			if pd.KeyContains != "" {
				t.Errorf("%s produced KeyContains = %q", expr, pd.KeyContains)
			}
		})
	}

	pd, err := ExtractObjectPushdown(`state == "AVAILABLE" && key.startsWith("a/") && key.contains("b")`)
	if err != nil {
		t.Fatalf("ExtractObjectPushdown: %v", err)
	}
	if pd.StateEq != "AVAILABLE" {
		t.Errorf("StateEq = %q, want AVAILABLE", pd.StateEq)
	}
	if pd.KeyPrefix != "a/" {
		t.Errorf("KeyPrefix = %q, want a/", pd.KeyPrefix)
	}
	if pd.KeyContains != "b" {
		t.Errorf("KeyContains = %q, want b", pd.KeyContains)
	}
}

// Both extractors keep the FIRST of a repeated predicate and ignore the rest.
// Taking the last instead would be defensible; taking both is not, since only
// one slot exists — and dropping the "already set" check silently lets a
// later conjunct overwrite an earlier one, narrowing to a predicate the
// caller wrote second rather than the conjunction of the two.
func TestPushdown_RepeatedPredicatesKeepTheFirst(t *testing.T) {
	ap, err := ExtractAuditPushdown(`action.startsWith("first") && action.startsWith("second")`)
	if err != nil {
		t.Fatalf("ExtractAuditPushdown: %v", err)
	}
	if ap.ActionPrefix != "first" {
		t.Errorf("ActionPrefix = %q, want first", ap.ActionPrefix)
	}

	op, err := ExtractObjectPushdown(
		`state == "FIRST" && state == "SECOND" && key.startsWith("a/") && key.startsWith("b/")`)
	if err != nil {
		t.Fatalf("ExtractObjectPushdown: %v", err)
	}
	if op.StateEq != "FIRST" {
		t.Errorf("StateEq = %q, want FIRST", op.StateEq)
	}
	if op.KeyPrefix != "a/" {
		t.Errorf("KeyPrefix = %q, want a/", op.KeyPrefix)
	}
}
