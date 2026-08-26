package cel

import "testing"

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
