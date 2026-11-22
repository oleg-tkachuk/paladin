package storagepath

import (
	"errors"
	"strings"
	"testing"
)

// composeSeeds are the inputs the old composer rewrote onto another object's
// path, beside canonical ones.
var composeSeeds = [][2]string{
	{"clean", "a/b/c"},
	{"../other", "../../x"},
	{"/leading", "./rel"},
	{"a//b", "../../etc/passwd"},
	{"team/docs", ".."},
	{"a/b", "c"},
	{"a", "b/c"},
	{"docs", "a/./b"},
	{"docs", "a//b"},
	{"docs", "dir/"},
	{"", ""},
}

// Compose either refuses its input or returns a path that is under the
// tenant's prefix, holds no segment a reader would resolve, and is the input
// the boundary validators accept — never a rewrite of it.
func FuzzCompose(f *testing.F) {
	for _, c := range composeSeeds {
		f.Add(c[0], c[1])
	}
	prefix := testTenant.String() + Separator
	f.Fuzz(func(t *testing.T, collection, key string) {
		got, err := Compose(testTenant, collection, key)
		canonical := ValidateCollection(collection) == nil && ValidateKey(key) == nil
		if !canonical {
			if !errors.Is(err, ErrNotCanonical) {
				t.Fatalf("Compose(%q, %q) = %q, %v: a non-canonical input was not refused", collection, key, got, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Compose(%q, %q) refused a canonical input: %v", collection, key, err)
		}
		rest, ok := strings.CutPrefix(got, prefix)
		if !ok {
			t.Fatalf("Compose(%q, %q) = %q, outside %q", collection, key, got, prefix)
		}
		for seg := range strings.SplitSeq(rest, Separator) {
			if seg == "" || seg == currentSegment || seg == parentSegment {
				t.Fatalf("Compose(%q, %q) = %q holds segment %q", collection, key, got, seg)
			}
		}
		segment, tail, _ := strings.Cut(rest, Separator)
		back, err := ParseCollectionSegment(segment)
		if err != nil || back != collection || tail != key {
			t.Fatalf("Compose(%q, %q) = %q does not parse back: %q, %q, %v", collection, key, got, back, tail, err)
		}
	})
}

// Two different canonical (collection, key) pairs never compose to one path,
// so no two objects share bytes.
func FuzzComposeInjective(f *testing.F) {
	for _, a := range composeSeeds {
		for _, b := range composeSeeds {
			f.Add(a[0], a[1], b[0], b[1])
		}
	}
	f.Fuzz(func(t *testing.T, c1, k1, c2, k2 string) {
		if c1 == c2 && k1 == k2 {
			return
		}
		p1, err1 := Compose(testTenant, c1, k1)
		p2, err2 := Compose(testTenant, c2, k2)
		if err1 != nil || err2 != nil {
			return
		}
		if p1 == p2 {
			t.Fatalf("(%q, %q) and (%q, %q) both compose to %q", c1, k1, c2, k2, p1)
		}
	})
}
