package adapters

import (
	"testing"
)

// A request's prefix is a conjunct of every branch: a branch that already
// demands more keeps it, one that contradicts it matches nothing and goes.
func TestNarrowToPrefix(t *testing.T) {
	branches := objectBranchHints(`key.startsWith("a/b/") || key.startsWith("z/") || state == "AVAILABLE"`)
	got := narrowToPrefix(branches, "a/")
	if len(got) != 2 {
		t.Fatalf("branches = %d, want the two that agree with a/", len(got))
	}
	for i, want := range []string{"a/b/", "a/"} {
		if got[i].rawPrefix != want || got[i].prefix == nil || *got[i].prefix != want {
			t.Errorf("branch %d prefix = %q, want %q", i, got[i].rawPrefix, want)
		}
	}
	if got := narrowToPrefix(objectBranchHints(`key.startsWith("z/")`), "a/"); len(got) != 0 {
		t.Errorf("a contradicting branch survived: %+v", got)
	}
	if got := narrowToPrefix(branches, ""); len(got) != len(branches) {
		t.Errorf("no prefix dropped branches")
	}
}

// The pushdown prefix is a LIKE pattern; the raw one is what is measured.
func TestNarrowToPrefixEscapesForLike(t *testing.T) {
	got := narrowToPrefix(objectBranchHints(""), "a_b%/")
	if *got[0].prefix != `a\_b\%/` || got[0].rawPrefix != "a_b%/" {
		t.Errorf("prefix = %q raw %q", *got[0].prefix, got[0].rawPrefix)
	}
}

func TestLongerPrefix(t *testing.T) {
	for _, c := range []struct {
		a, b, want string
		ok         bool
	}{
		{"a/b/", "a/", "a/b/", true},
		{"a/", "a/b/", "a/b/", true},
		{"", "a/", "a/", true},
		{"a/", "b/", "", false},
	} {
		got, ok := longerPrefix(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("longerPrefix(%q, %q) = %q, %v; want %q, %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestWithLevel(t *testing.T) {
	if got := withLevel(objectBranchHints(""), "a/", ""); got[0].level != nil || got[0].delimiter != nil {
		t.Errorf("no delimiter set a level")
	}
	got := withLevel(objectBranchHints(`state == "A" || state == "B"`), "a/", "/")
	for i, h := range got {
		if h.level == nil || *h.level != "a/" || h.delimiter == nil || *h.delimiter != "/" {
			t.Errorf("branch %d level = %v delimiter = %v", i, h.level, h.delimiter)
		}
	}
}

func TestAtLevel(t *testing.T) {
	for _, c := range []struct {
		key, prefix, delimiter string
		want                   bool
	}{
		{"a/x.txt", "a/", "/", true},
		{"a/b/x.txt", "a/", "/", false},
		{"b/x.txt", "a/", "/", false},
		{"a/b/x.txt", "a/", "", true},
		{"top.txt", "", "/", true},
		{"a/x.txt", "", "/", false},
	} {
		if got := atLevel(c.key, c.prefix, c.delimiter); got != c.want {
			t.Errorf("atLevel(%q, %q, %q) = %v, want %v", c.key, c.prefix, c.delimiter, got, c.want)
		}
	}
}

// Folders follow the state a filter names: one, when every branch names the
// same; none, when they differ or one names no state.
func TestFilterState(t *testing.T) {
	for _, c := range []struct {
		filter string
		want   string
	}{
		{`state == "DELETED"`, "DELETED"},
		{`state == "DELETED" && key.contains("x")`, "DELETED"},
		{`state in ["DELETED", "AVAILABLE"]`, ""},
		{`state == "DELETED" || key.contains("x")`, ""},
		{``, ""},
	} {
		got := filterState(c.filter)
		if (got == nil && c.want != "") || (got != nil && *got != c.want) {
			t.Errorf("filterState(%q) = %v, want %q", c.filter, got, c.want)
		}
	}
}
