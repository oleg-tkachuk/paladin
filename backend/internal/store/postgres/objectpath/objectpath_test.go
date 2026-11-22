package objectpath

import (
	"testing"
	"uuid"
)

func TestLockKey(t *testing.T) {
	tenant := uuid.MustParse("0192f6a4-0000-7000-8000-000000000001")
	other := uuid.MustParse("0192f6a4-0000-7000-8000-000000000002")
	base := lockKey(tenant, "docs", "a/b.txt")

	if again := lockKey(tenant, "docs", "a/b.txt"); again != base {
		t.Fatalf("the same path gave two keys: %d and %d — an insert and a purge would not exclude each other", base, again)
	}
	distinct := map[string]int64{
		"another tenant":                lockKey(other, "docs", "a/b.txt"),
		"another collection":            lockKey(tenant, "doc", "a/b.txt"),
		"another path":                  lockKey(tenant, "docs", "a/c.txt"),
		"the boundary moved across a /": lockKey(tenant, "docs/a", "b.txt"),
	}
	for what, key := range distinct {
		if key == base {
			t.Errorf("%s shares the path's lock key", what)
		}
	}
}

// A debt whose key the composer refuses names no bytes of its own: Check
// settles it without a query, so a purge never retries what cannot succeed.
func TestCheckSettlesAnUnaddressablePath(t *testing.T) {
	tenant := uuid.MustParse("0192f6a4-0000-7000-8000-000000000001")
	for _, tc := range [][2]string{
		{"docs", "a//b"},
		{"docs", "a/./b"},
		{"docs", "/a"},
		{"docs", ".."},
		{"docs", ""},
	} {
		got, err := Check(t.Context(), nil, tenant, tc[0], tc[1])
		if err != nil || got != Unaddressable {
			t.Errorf("Check(%q, %q) = %v, %v, want Unaddressable", tc[0], tc[1], got, err)
		}
	}
}
