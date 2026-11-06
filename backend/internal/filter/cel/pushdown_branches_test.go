package cel

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// A disjunction across fields is split into one branch per arm, each a
// narrowing the query can run; a conjunct beside it narrows every branch.
func TestExtractPushdownBranches(t *testing.T) {
	for name, tc := range map[string]struct {
		expr string
		want []string // per branch, the pushed slug and display_name values
	}{
		"no filter":            {``, []string{"|"}},
		"one conjunct":         {`slug == "a"`, []string{"a|"}},
		"across two fields":    {`slug == "a" || display_name == "B"`, []string{"a|", "|B"}},
		"one field stays one":  {`slug == "a" || slug == "b"`, []string{"a,b|"}},
		"in stays one":         {`slug in ["a", "b"] || display_name == "B"`, []string{"a,b|", "|B"}},
		"conjunct distributes": {`(slug == "a" || display_name == "B") && slug.startsWith("x")`, []string{"a|", "|B"}},
		"unconstrained arm":    {`slug == "a" || size(labels) > 1`, []string{"|"}},
		"nested":               {`(slug == "a" || slug == "b") || display_name == "B"`, []string{"a,b|", "|B"}},
	} {
		t.Run(name, func(t *testing.T) {
			branches, err := ExtractPushdownBranches(TenantSchema, tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, b := range branches {
				got = append(got, fmt.Sprintf("%s|%s", join(b.In["slug"]), join(b.In["display_name"])))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("branches %v, want %v", got, tc.want)
			}
		})
	}
}

func join(vs []string) string {
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

// A conjunct shared by two branches is copied into each: narrowing one
// branch later must not reach its sibling.
func TestPushdownBranchesDoNotShareMaps(t *testing.T) {
	branches, err := ExtractPushdownBranches(TenantSchema,
		`slug.startsWith("x") && (slug == "a" || display_name == "B")`)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 {
		t.Fatalf("%d branches, want 2", len(branches))
	}
	branches[0].Prefix["slug"] = "changed"
	if branches[1].Prefix["slug"] != "x" {
		t.Errorf("a sibling branch saw the change: %v", branches[1].Prefix)
	}
}

// Past the cap the filter is cheaper to scan than to fan out: one branch
// that narrows nothing.
func TestPushdownBranchesCap(t *testing.T) {
	expr := ""
	for i := range maxPushdownBranches + 1 {
		if i > 0 {
			expr += " || "
		}
		if i%2 == 0 {
			expr += fmt.Sprintf("slug == %q", fmt.Sprint(i))
		} else {
			expr += fmt.Sprintf("display_name == %q", fmt.Sprint(i))
		}
	}
	branches, err := ExtractPushdownBranches(TenantSchema, expr)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || !branches[0].empty() {
		t.Errorf("%d branches past the cap, want one unconstrained", len(branches))
	}
}

// The union of what the branches fetch holds every row the filter accepts,
// over rows matching one arm, the other, both and neither.
func TestPushdownBranchesNeverExcludeARowTheFilterAccepts(t *testing.T) {
	at := mustTime(t, "2026-03-01T12:00:00Z")
	type row struct{ slug, display string }
	rows := []row{{"a", "A"}, {"z", "B"}, {"a", "B"}, {"z", "Z"}}
	filters := []string{
		`slug == "a" || display_name == "B"`,
		`(slug == "a" || display_name == "B") && slug != "q"`,
		`slug.startsWith("a") || display_name in ["B", "C"]`,
		`!(slug == "a") || display_name == "B"`,
	}
	ev := NewEvaluator()
	for _, expr := range filters {
		t.Run(expr, func(t *testing.T) {
			branches, err := ExtractPushdownBranches(TenantSchema, expr)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				page := []map[string]any{{
					"tenant_id": "t", "slug": r.slug, "display_name": r.display,
					"storage_layout": "shared", "labels": map[string]string{},
					"created_at": at, "updated_at": at,
				}}
				kept, err := FilterPage(ev, TenantSchema, expr, page,
					func(m map[string]any) map[string]any { return m })
				if err != nil {
					t.Fatal(err)
				}
				fetched := slices.ContainsFunc(branches, func(b Pushdown) bool {
					return fetches(b, r.slug, r.display, at)
				})
				if len(kept) == 1 && !fetched {
					t.Errorf("row %+v is accepted but no branch fetches it: %+v", r, branches)
				}
			}
		})
	}
}

// fetches is what the tenant query's predicates select, for the fields the
// test rows carry.
func fetches(b Pushdown, slug, display string, _ time.Time) bool {
	matches := func(field, v string) bool {
		if in, ok := b.In[field]; ok && !slices.Contains(in, v) {
			return false
		}
		if p, ok := b.Prefix[field]; ok && (len(v) < len(p) || v[:len(p)] != p) {
			return false
		}
		if n, ok := b.Neq[field]; ok && v == n {
			return false
		}
		return true
	}
	return matches("slug", slug) && matches("display_name", display)
}
