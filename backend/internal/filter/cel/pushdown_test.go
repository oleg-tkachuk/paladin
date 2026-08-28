package cel

import (
	"testing"
	"time"
)

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
				if p.Recognised != 2 {
					t.Fatalf("recognised %d, want 2", p.Recognised)
				}
				eq, like := p.StringHint("provider")
				if eq == nil || *eq != "s3" {
					t.Errorf("provider eq = %v, want s3", eq)
				}
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
				eq, _ := p.StringHint("state")
				if eq == nil || *eq != "FAILED" {
					t.Errorf("state eq = %v, want FAILED", eq)
				}
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
				if eq, _ := p.StringHint("error_message"); eq != nil {
					t.Errorf("an inequality was recorded as an equality: %q", *eq)
				}
			},
		},
		{
			name:   "a disjunction pushes nothing — either side may match",
			schema: OperationSchema,
			expr:   `state == "FAILED" || state == "CANCELLED"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if p.Recognised != 0 {
					t.Errorf("pushed %d predicates out of an OR", p.Recognised)
				}
				if eq, _ := p.StringHint("state"); eq != nil {
					t.Errorf("state eq = %q — one arm of an OR is not a filter", *eq)
				}
			},
		},
		{
			name:   "an OR nested in an AND keeps the AND's own conjuncts",
			schema: OperationSchema,
			expr:   `type == "BatchCopy" && (state == "FAILED" || state == "RUNNING")`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				eq, _ := p.StringHint("type")
				if eq == nil || *eq != "BatchCopy" {
					t.Errorf("type eq = %v, want BatchCopy", eq)
				}
				if eq, _ := p.StringHint("state"); eq != nil {
					t.Errorf("state eq = %q, from inside an OR", *eq)
				}
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
				if p.Recognised != 0 {
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
				if p.Recognised != 0 {
					t.Errorf("recognised %d type-mismatched predicates", p.Recognised)
				}
			},
		},
		{
			name:   "contradictory equalities keep the first",
			schema: OperationSchema,
			expr:   `state == "FAILED" && state == "RUNNING"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				eq, _ := p.StringHint("state")
				if eq == nil || *eq != "FAILED" {
					t.Errorf("state eq = %v, want the first conjunct", eq)
				}
			},
		},
		{
			name:   "timestamps and maps stay in the authoritative pass",
			schema: TenantSchema,
			expr:   `slug.startsWith("acme") && labels["tier"] == "gold"`,
			check: func(t *testing.T, p Pushdown) {
				t.Helper()
				if p.Recognised != 1 {
					t.Errorf("recognised %d, want only the slug prefix", p.Recognised)
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
	if p.Recognised != 0 {
		t.Errorf("pushed %d predicates out of an unparsable filter", p.Recognised)
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
	if pd.Recognised != 2 {
		t.Errorf("Recognised = %d, want 2", pd.Recognised)
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
	if pd.Recognised != 0 {
		t.Errorf("Recognised = %d, want 0", pd.Recognised)
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
	if pd.Recognised != 1 {
		t.Errorf("Recognised = %d, want 1 — the duplicate is not a second predicate", pd.Recognised)
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
