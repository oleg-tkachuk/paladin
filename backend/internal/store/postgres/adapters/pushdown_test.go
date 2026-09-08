package adapters

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// A pushdown that narrows nothing is always correct — the authoritative CEL
// pass runs afterwards either way — so both of these swallow their failures.
// What must not happen is a pushdown that narrows *wrongly*: the predicate
// goes into the SQL, and rows it excludes never reach the CEL pass to be
// reconsidered.

func TestHintsNarrowsNothingOnBadInput(t *testing.T) {
	if got := hints(cel.ObjectSchema, ""); got.Recognised != 0 {
		t.Errorf("hints(empty filter) = %+v, want a zero pushdown", got)
	}
	if got := hints(cel.ObjectSchema, `key == (((`); got.Recognised != 0 {
		t.Errorf("hints(unparseable) = %+v, want a zero pushdown", got)
	}

	// The point of the function: a filter the extractor understands must
	// actually produce hints, or every List silently falls back to paging
	// through the whole table in memory.
	if got := hints(cel.ObjectSchema, `key == "a"`); got.Recognised == 0 {
		t.Error("hints(key == \"a\") is empty — nothing would be pushed into SQL")
	}
}

func TestCreatedBounds(t *testing.T) {
	gte, lte := createdBounds(cel.Pushdown{})
	if gte.Valid || lte.Valid {
		t.Errorf("createdBounds(zero) = (%+v, %+v), want both NULL", gte, lte)
	}

	lo := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	hi := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	pd, err := cel.ExtractPushdown(
		cel.ObjectSchema,
		`created_at > timestamp("2026-01-02T03:04:05Z") && created_at < timestamp("2026-02-03T04:05:06Z")`,
	)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}

	gte, lte = createdBounds(pd)
	if !gte.Valid || !gte.Time.Equal(lo) {
		t.Errorf("lower bound = %+v, want %v", gte, lo)
	}
	if !lte.Valid || !lte.Time.Equal(hi) {
		t.Errorf("upper bound = %+v, want %v", lte, hi)
	}

	// One-sided ranges must leave the other side unbounded rather than
	// pinning it to the zero time, which would exclude every row.
	onlyLo, err := cel.ExtractPushdown(cel.ObjectSchema, `created_at > timestamp("2026-01-02T03:04:05Z")`)
	if err != nil {
		t.Fatalf("ExtractPushdown: %v", err)
	}
	gte, lte = createdBounds(onlyLo)
	if !gte.Valid {
		t.Error("lower bound dropped on a one-sided range")
	}
	if lte.Valid {
		t.Errorf("upper bound = %+v on a one-sided range, want NULL", lte)
	}
}

// likeLiteral decides whether a caller's literal is safe to hand to SQL LIKE.
// The metacharacter check is the guard: a `_` pushed down unescaped matches
// any single character, so the query returns rows the filter rejects — and
// unlike a too-wide scan, the CEL pass cannot put back rows a wrong predicate
// let in, it can only fail to remove them if the caller trusted the page.
func TestLikeLiteral(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
		ok   bool
	}{
		"plain":         {"report", "report", true},
		"empty":         {"", "", false},
		"underscore":    {"a_b", "", false},
		"percent":       {"50%", "", false},
		"backslash":     {`c:\x`, "", false},
		"metachar only": {"_", "", false},
		// Nothing in the set is a metacharacter, so a literal built from
		// neighbouring punctuation must still be pushed down.
		"punctuation": {"a-b.c/d", "a-b.c/d", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := likeLiteral(tc.in)
			if ok != tc.ok {
				t.Fatalf("likeLiteral(%q): ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("likeLiteral(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
