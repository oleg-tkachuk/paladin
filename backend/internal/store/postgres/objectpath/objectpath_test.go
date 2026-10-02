package objectpath

import (
	"testing"

	"github.com/google/uuid"
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
