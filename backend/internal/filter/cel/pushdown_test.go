package cel

import (
	"maps"
	"slices"
	"testing"
	"time"
)

// wantValues checks the set of values a field was pushed to take; nil means
// no value constraint at all.
func wantValues(t *testing.T, p Pushdown, field string, want []string) {
	t.Helper()
	got, _ := p.StringHint(field)
	if !slices.Equal(got, want) {
		t.Errorf("%s in %q, want %q", field, got, want)
	}
}

// The walk has to hold one line: a predicate either becomes a SQL hint that
// means exactly what the CEL meant, or it becomes nothing. A hint that is
// merely close is worse than none, because the authoritative pass can only
// remove rows the pushdown let through — never bring back one it excluded.
func TestExtractPushdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *Schema
		expr   string
		check  func(t *testing.T, p Pushdown)
	}{
		{
			name:   "string equality and prefix on the same schema",
			schema: StorageBackendSchema,
			expr:   `provider == "s3" && display_name.startsWith("prod")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 2 {
					t.Fatalf("recognised %d, want 2", predicates(p))
				}
				wantValues(t, p, "provider", []string{"s3"})
				_, like := p.StringHint("provider")
				if like != nil {
					t.Errorf("provider like = %v, want none", *like)
				}
				_, like = p.StringHint("display_name")
				if like == nil || *like != "prod%" {
					t.Errorf("display_name like = %v, want prod%%", like)
				}
			},
		},
		{
			name:   "contains becomes an unanchored pattern",
			schema: PhysicalBucketSchema,
			expr:   `display_name.contains("archive")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				_, like := p.StringHint("display_name")
				if like == nil || *like != "%archive%" {
					t.Errorf("like = %v, want %%archive%%", like)
				}
			},
		},
		{
			name:   "booleans, written both ways",
			schema: StorageBackendSchema,
			expr:   `enabled && read_only == false && !maintenance`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				for field, want := range map[string]bool{
					"enabled": true, "read_only": false, "maintenance": false,
				} {
					got := p.BoolHint(field)
					if got == nil || *got != want {
						t.Errorf("%s = %v, want %v", field, got, want)
					}
				}
			},
		},
		{
			name:   "reversed operands are the same predicate",
			schema: OperationSchema,
			expr:   `"FAILED" == state`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", []string{"FAILED"})
			},
		},
		{
			name:   "inequality is pushed, and is not a negated equality",
			schema: OperationSchema,
			expr:   `error_message != ""`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				// The domain projects a NULL column to "" before CEL sees it,
				// so the SQL has to be over coalesce(col, ''), not over col —
				// which is why this is its own hint and not Eq inverted.
				got := p.NeqHint("error_message")
				if got == nil || *got != "" {
					t.Errorf("error_message neq = %v, want the empty literal", got)
				}
				wantValues(t, p, "error_message", nil)
			},
		},
		{
			name:   "a disjunction of one field's values is the set of them",
			schema: OperationSchema,
			expr:   `state == "FAILED" || "CANCELLED" == state || state == "FAILED"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", []string{"FAILED", "CANCELLED"})
			},
		},
		{
			name:   "in is the same set, and mixes with equalities in a disjunction",
			schema: OperationSchema,
			expr:   `state in ["FAILED", "RUNNING"] || state == "PENDING"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", []string{"FAILED", "RUNNING", "PENDING"})
			},
		},
		{
			name:   "a disjunction across two fields pushes nothing — either side may match",
			schema: OperationSchema,
			expr:   `state == "FAILED" || type == "BatchCopy"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 0 {
					t.Errorf("pushed %d predicates out of an OR over two fields", predicates(p))
				}
			},
		},
		{
			name:   "a disjunction with anything but equalities pushes nothing",
			schema: OperationSchema,
			expr:   `state == "FAILED" || state.startsWith("RUN")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 0 {
					t.Errorf("pushed %d predicates out of an OR with a prefix arm", predicates(p))
				}
			},
		},
		{
			name:   "an in over anything but string literals pushes nothing",
			schema: OperationSchema,
			expr:   `state in ["FAILED", type]`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", nil)
			},
		},
		{
			name:   "an empty in is left to the authoritative pass",
			schema: OperationSchema,
			expr:   `state in []`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", nil)
			},
		},
		{
			name:   "an OR nested in an AND joins the AND's own conjuncts",
			schema: OperationSchema,
			expr:   `type == "BatchCopy" && (state == "FAILED" || state == "RUNNING")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "type", []string{"BatchCopy"})
				wantValues(t, p, "state", []string{"FAILED", "RUNNING"})
			},
		},
		{
			name:   "LIKE metacharacters are dropped, not escaped",
			schema: PhysicalBucketSchema,
			expr:   `display_name.startsWith("100%_of")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if _, like := p.StringHint("display_name"); like != nil {
					t.Errorf("like = %q — %% and _ would widen the scan while "+
						"looking precise", *like)
				}
			},
		},
		{
			name:   "a field the schema does not declare is not a column",
			schema: PhysicalBucketSchema,
			expr:   `nonexistent == "x"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 0 {
					t.Errorf("recognised an undeclared field")
				}
			},
		},
		{
			name:   "a type mismatch is not pushed",
			schema: StorageBackendSchema,
			expr:   `enabled == "yes" && provider == true`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 0 {
					t.Errorf("recognised %d type-mismatched predicates", predicates(p))
				}
			},
		},
		{
			name:   "contradictory equalities keep the first",
			schema: OperationSchema,
			expr:   `state == "FAILED" && state == "RUNNING"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				wantValues(t, p, "state", []string{"FAILED"})
			},
		},
		{
			name:   "a label entry is pushed beside a column",
			schema: TenantSchema,
			expr:   `slug.startsWith("acme") && labels["tier"] == "gold"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if predicates(p) != 2 || p.MapEq["labels"]["tier"] != "gold" {
					t.Errorf("recognised %d (%v), want the slug prefix and the label", predicates(p), p.MapEq)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := ExtractPushdown(tc.schema, tc.expr)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			tc.check(t, p)
		})
	}
}

// A filter that does not parse is the compiler's problem to report, not the
// pushdown's: it must not panic, and it must push nothing.
func TestExtractPushdownRejectsGarbageWithoutPanicking(t *testing.T) {
	t.Parallel()
	p, err := ExtractPushdown(OperationSchema, `state == ((`)
	if err == nil {
		t.Error("a parse error went unreported")
	}
	if predicates(p) != 0 {
		t.Errorf("pushed %d predicates out of an unparsable filter", predicates(p))
	}
}

// ─── timestamp ranges ──────────────────────────────────────────────────────

// Timestamp comparisons were the largest remaining hole: every list schema
// exposes created_at, and a filter made only of one read the whole table a
// page at a time. AuditPushdown had them for its own schema; this is the same
// recognition generalised over any timestamp-typed field.

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

func TestPushdownRecognisesTimestampBounds(t *testing.T) {
	pd, err := ExtractPushdown(StorageBackendSchema,
		`created_at >= timestamp("2026-01-01T00:00:00Z") && created_at <= timestamp("2026-06-30T23:59:59Z")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	gte, lte := pd.TimeHint("created_at")
	if gte == nil || !gte.Equal(mustTime(t, "2026-01-01T00:00:00Z")) {
		t.Errorf("gte = %v", gte)
	}
	if lte == nil || !lte.Equal(mustTime(t, "2026-06-30T23:59:59Z")) {
		t.Errorf("lte = %v", lte)
	}
	if predicates(pd) != 2 {
		t.Errorf("predicates = %d, want 2", predicates(pd))
	}
}

func TestPushdownWidensStrictTimestampBounds(t *testing.T) {
	// `>` is pushed as `>=`. The pushdown only narrows the candidate set, so
	// including the boundary instant costs one row that the authoritative CEL
	// pass then rejects. Pushing the strict form would be the unsafe
	// direction: it could drop a row the filter accepts.
	pd, err := ExtractPushdown(StorageBackendSchema, `created_at > timestamp("2026-01-01T00:00:00Z")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	gte, lte := pd.TimeHint("created_at")
	if gte == nil || !gte.Equal(mustTime(t, "2026-01-01T00:00:00Z")) {
		t.Errorf("strict > did not widen to >=: %v", gte)
	}
	if lte != nil {
		t.Errorf("lte = %v, want unbounded", lte)
	}
}

func TestPushdownAcceptsTimestampOperandsInEitherOrder(t *testing.T) {
	// `timestamp(x) <= created_at` is the same predicate as
	// `created_at >= timestamp(x)`; CEL does not normalise the order.
	pd, err := ExtractPushdown(StorageBackendSchema, `timestamp("2026-01-01T00:00:00Z") <= created_at`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	gte, lte := pd.TimeHint("created_at")
	if gte == nil || !gte.Equal(mustTime(t, "2026-01-01T00:00:00Z")) {
		t.Errorf("flipped operands lost the lower bound: gte=%v lte=%v", gte, lte)
	}
	if lte != nil {
		t.Errorf("flipped operands produced an upper bound: %v", lte)
	}
}

func TestPushdownIgnoresTimestampBoundsOnNonTimestampFields(t *testing.T) {
	// display_name is a string; a comparison against it is legal CEL and must
	// simply not become a SQL range.
	pd, err := ExtractPushdown(StorageBackendSchema, `display_name >= "m"`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	if gte, lte := pd.TimeHint("display_name"); gte != nil || lte != nil {
		t.Errorf("pushed a range over a string field: %v / %v", gte, lte)
	}
	if predicates(pd) != 0 {
		t.Errorf("predicates = %d, want 0", predicates(pd))
	}
}

func TestPushdownKeepsTheFirstOfDuplicateBounds(t *testing.T) {
	// Two lower bounds could be merged to the tighter one, but choosing
	// silently would make the pushed predicate depend on conjunct order. The
	// in-memory pass applies both regardless, so keeping the first is correct
	// and stable.
	pd, err := ExtractPushdown(StorageBackendSchema,
		`created_at >= timestamp("2026-01-01T00:00:00Z") && created_at >= timestamp("2026-03-01T00:00:00Z")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	gte, _ := pd.TimeHint("created_at")
	if gte == nil || !gte.Equal(mustTime(t, "2026-01-01T00:00:00Z")) {
		t.Errorf("gte = %v, want the first bound", gte)
	}
	if predicates(pd) != 1 {
		t.Errorf("predicates = %d, want 1 — the duplicate is not a second predicate", predicates(pd))
	}
}

func TestPushdownIgnoresAMalformedTimestampLiteral(t *testing.T) {
	pd, err := ExtractPushdown(StorageBackendSchema, `created_at >= timestamp("not-a-time")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	if gte, _ := pd.TimeHint("created_at"); gte != nil {
		t.Errorf("pushed a bound from an unparseable literal: %v", gte)
	}
}

// The pushdown's whole safety argument is one-directional: SQL may return
// rows the CEL then rejects, but it must never withhold a row the CEL would
// have accepted — the authoritative pass runs after the query and cannot
// bring back what the query did not fetch. Everything else in this file
// checks that a predicate is recognised; this checks that recognising it
// stayed sound, which is the property a future widening of the walk is most
// likely to break.
func TestPushdownNeverExcludesARowTheFilterAccepts(t *testing.T) {
	base := mustTime(t, "2026-03-01T12:00:00Z")
	rows := []time.Time{
		base.Add(-48 * time.Hour),
		base.Add(-time.Nanosecond),
		base, // the boundary instant, where a strict bound would differ
		base.Add(time.Nanosecond),
		base.Add(48 * time.Hour),
	}
	filters := []string{
		`created_at >= timestamp("2026-03-01T12:00:00Z")`,
		`created_at > timestamp("2026-03-01T12:00:00Z")`,
		`created_at <= timestamp("2026-03-01T12:00:00Z")`,
		`created_at < timestamp("2026-03-01T12:00:00Z")`,
		`timestamp("2026-03-01T12:00:00Z") <= created_at`,
		`created_at >= timestamp("2026-02-01T00:00:00Z") && created_at <= timestamp("2026-04-01T00:00:00Z")`,
	}

	ev := NewEvaluator()

	for _, expr := range filters {
		t.Run(expr, func(t *testing.T) {
			pd, err := ExtractPushdown(TenantSchema, expr)
			if err != nil {
				t.Fatalf("ExtractPushdown: %v", err)
			}
			gte, lte := pd.TimeHint("created_at")

			for _, at := range rows {
				// What the authoritative pass decides.
				page := []map[string]any{{
					"tenant_id": "t", "slug": "s", "display_name": "s",
					"storage_layout": "shared", "labels": map[string]string{},
					"created_at": at, "updated_at": at,
				}}
				kept, err := FilterPage(ev, TenantSchema, expr, page,
					func(r map[string]any) map[string]any { return r })
				if err != nil {
					t.Fatalf("FilterPage: %v", err)
				}
				accepted := len(kept) == 1

				// What the query would have fetched.
				fetched := (gte == nil || !at.Before(*gte)) && (lte == nil || !at.After(*lte))

				if accepted && !fetched {
					t.Errorf("row at %s is accepted by the filter but excluded by the pushed bounds [%v, %v]",
						at.Format(time.RFC3339Nano), gte, lte)
				}
			}
		})
	}
}

// predicates counts what a pushdown would put into SQL: every field it holds a
// predicate or a bound for.
func predicates(p Pushdown) int {
	return len(p.In) + len(p.Neq) + len(p.BoolEq) + len(p.Prefix) +
		len(p.Contains) + len(p.TimeGTE) + len(p.TimeLTE) + len(p.MapEq)
}

// The same invariant for value sets: whatever `in` or a one-field disjunction
// pushes, a row the filter accepts is among the rows the query fetches.
func TestPushdownValueSetsNeverExcludeARowTheFilterAccepts(t *testing.T) {
	at := mustTime(t, "2026-03-01T12:00:00Z")
	rows := []map[string]any{}
	for _, slug := range []string{"a", "b", "c"} {
		for _, layout := range []string{"shared", "dedicated"} {
			rows = append(rows, map[string]any{
				"tenant_id": "t-" + slug, "slug": slug, "display_name": slug,
				"storage_layout": layout, "labels": map[string]string{},
				"created_at": at, "updated_at": at,
			})
		}
	}
	filters := []string{
		`slug in ["a", "b"]`,
		`slug == "a" || slug == "c"`,
		`slug == "a" || storage_layout == "dedicated"`,
		`(slug == "a" || slug == "b") && slug == "b"`,
		`slug in ["a"] && slug == "b"`,
		`slug in ["a", "b"] && storage_layout in ["dedicated"]`,
		`!(slug in ["a"])`,
	}

	ev := NewEvaluator()
	for _, expr := range filters {
		t.Run(expr, func(t *testing.T) {
			pd, err := ExtractPushdown(TenantSchema, expr)
			if err != nil {
				t.Fatalf("ExtractPushdown: %v", err)
			}
			for _, row := range rows {
				kept, err := FilterPage(ev, TenantSchema, expr, []map[string]any{row},
					func(r map[string]any) map[string]any { return r })
				if err != nil {
					t.Fatalf("FilterPage: %v", err)
				}
				fetched := true
				for field, values := range pd.In {
					if !slices.Contains(values, row[field].(string)) {
						fetched = false
					}
				}
				if len(kept) == 1 && !fetched {
					t.Errorf("row %v is accepted by the filter but excluded by the pushed sets %v", row, pd.In)
				}
			}
		})
	}
}

// An entry of a string map compared to a literal is a containment the query
// can answer: `labels["env"] == "prod"` selects the rows whose labels hold
// that pair, whichever of the two spellings and operand orders is used.
func TestPushdownMapEntries(t *testing.T) {
	for name, tc := range map[string]struct {
		expr string
		want map[string]string
	}{
		"index":                {`labels["env"] == "prod"`, map[string]string{"env": "prod"}},
		"select":               {`labels.env == "prod"`, map[string]string{"env": "prod"}},
		"literal first":        {`"prod" == labels["env"]`, map[string]string{"env": "prod"}},
		"two keys":             {`labels["env"] == "prod" && labels["team"] == "core"`, map[string]string{"env": "prod", "team": "core"}},
		"first value of a key": {`labels["env"] == "prod" && labels["env"] == "dev"`, map[string]string{"env": "prod"}},
		"beside a column":      {`slug == "acme" && labels["env"] == "prod"`, map[string]string{"env": "prod"}},
	} {
		t.Run(name, func(t *testing.T) {
			pd, err := ExtractPushdown(TenantSchema, tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got := pd.MapEq["labels"]; !maps.Equal(got, tc.want) {
				t.Errorf("labels %v, want %v", got, tc.want)
			}
		})
	}
}

// Anything but a conjunct comparing one entry to a string literal stays with
// the CEL pass: inequality and disjunction would narrow wrongly, and a map
// field another schema does not declare is no column at all.
func TestPushdownMapEntriesLeftToCEL(t *testing.T) {
	for name, expr := range map[string]string{
		"inequality":         `labels["env"] != "prod"`,
		"the empty value":    `labels["env"] == ""`,
		"disjunction":        `labels["env"] == "prod" || labels["env"] == "dev"`,
		"across a column":    `labels["env"] == "prod" || slug == "acme"`,
		"key presence":       `"env" in labels`,
		"entry to entry":     `labels["env"] == labels["stage"]`,
		"non-string literal": `labels["env"] == 1`,
	} {
		t.Run(name, func(t *testing.T) {
			pd, err := ExtractPushdown(TenantSchema, expr)
			if err != nil {
				return // a parse failure pushes nothing either
			}
			if len(pd.MapEq) != 0 {
				t.Errorf("%s pushed %v", expr, pd.MapEq)
			}
		})
	}
	pd, err := ExtractPushdown(CollectionSchema, `labels["env"] == "prod"`)
	if err == nil && len(pd.MapEq) != 0 {
		t.Errorf("a schema without labels pushed %v", pd.MapEq)
	}
}

// The containment the query runs is one JSON object holding every pushed
// entry; nothing pushed is no predicate.
func TestMapContainsHint(t *testing.T) {
	pd, err := ExtractPushdown(TenantSchema, `labels["env"] == "prod" && labels["team"] == "core"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(pd.MapContainsHint("labels")); got != `{"env":"prod","team":"core"}` {
		t.Errorf("hint %s", got)
	}
	if got := (Pushdown{}).MapContainsHint("labels"); got != nil {
		t.Errorf("empty pushdown hint %s, want nil", got)
	}
}

// The label containment may only narrow: no row the authoritative pass
// accepts lacks a pushed entry. Rows cover an exact match, extra keys, the
// key with another value, the key missing and no labels at all.
func TestLabelPushdownNeverExcludesARowTheFilterAccepts(t *testing.T) {
	rows := []map[string]string{
		{"env": "prod"},
		{"env": "prod", "team": "core"},
		{"env": "dev"},
		{"team": "core"},
		{},
	}
	filters := []string{
		`labels["env"] == "prod"`,
		`labels.env == "prod" && labels["team"] == "core"`,
		`"prod" == labels["env"] || slug == "acme"`,
		`slug.startsWith("s") && labels["env"] == "prod"`,
		`labels["env"] == ""`,
		`labels.env == "" && labels["team"] == "core"`,
	}
	ev := NewEvaluator()
	at := mustTime(t, "2026-03-01T12:00:00Z")
	for _, expr := range filters {
		t.Run(expr, func(t *testing.T) {
			pd, err := ExtractPushdown(TenantSchema, expr)
			if err != nil {
				t.Fatalf("ExtractPushdown: %v", err)
			}
			for _, labels := range rows {
				page := []map[string]any{{
					"tenant_id": "t", "slug": "s", "display_name": "s",
					"storage_layout": "shared", "labels": labels,
					"created_at": at, "updated_at": at,
				}}
				kept, err := FilterPage(ev, TenantSchema, expr, page,
					func(r map[string]any) map[string]any { return r })
				if err != nil {
					t.Fatalf("FilterPage: %v", err)
				}
				accepted := len(kept) == 1
				fetched := true
				for k, v := range pd.MapEq["labels"] {
					if labels[k] != v {
						fetched = false
					}
				}
				if accepted && !fetched {
					t.Errorf("labels %v are accepted by the filter but excluded by the pushed %v", labels, pd.MapEq)
				}
			}
		})
	}
}
