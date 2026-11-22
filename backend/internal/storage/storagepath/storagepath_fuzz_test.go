package storagepath

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
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
// tenant's prefix, fits the store, holds no segment a reader would resolve,
// and is the input — never a rewrite of it. A key the boundary admits always
// composes; a key it refuses composes only when an object stored before the
// rule could hold it.
func FuzzCompose(f *testing.F) {
	for _, c := range composeSeeds {
		f.Add(c[0], c[1])
	}
	f.Add(longestCollection, longKey(MaxKeyBytes))
	f.Add(longestCollection, longKey(MaxKeyBytes+1))
	f.Add("docs", "a\x00b")
	f.Add("docs", "a"+invalidUTF8)
	prefix := testTenant.String() + Separator
	f.Fuzz(func(t *testing.T, collection, key string) {
		got, err := Compose(testTenant, collection, key)
		admitted := ValidateCollection(collection) == nil && ValidateKey(key) == nil
		if admitted && err != nil {
			t.Fatalf("Compose(%q, %q) refused an admitted input: %v", collection, key, err)
		}
		composable := ValidateCollection(collection) == nil && utf8.ValidString(key) && shapeReason(key) == "" &&
			len(prefix)+len(CollectionSegment(collection))+len(Separator)+len(key) <= MaxPathBytes
		if !composable {
			if !errors.Is(err, ErrNotCanonical) {
				t.Fatalf("Compose(%q, %q) = %q, %v: an input the store cannot hold apart was not refused", collection, key, got, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Compose(%q, %q) refused a composable input: %v", collection, key, err)
		}
		if len(got) > MaxPathBytes || !utf8.ValidString(got) {
			t.Fatalf("Compose(%q, %q) = %d bytes, valid UTF-8 %v: the store cannot hold it", collection, key, len(got), utf8.ValidString(got))
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

// A key the boundary admits is UTF-8 text without a control character, in
// segments a filer-backed store takes, and fits the store in every
// collection, the longest one included.
func FuzzValidateKey(f *testing.F) {
	for _, c := range composeSeeds {
		f.Add(c[1])
	}
	f.Add(longKey(MaxKeyBytes))
	f.Add(strings.Repeat("é", MaxSegmentBytes/len("é")+1))
	f.Add("a\u0085b")
	f.Fuzz(func(t *testing.T, key string) {
		if ValidateKey(key) != nil {
			return
		}
		if !utf8.ValidString(key) || len(key) > MaxKeyBytes || strings.ContainsFunc(key, unicode.IsControl) {
			t.Fatalf("ValidateKey admitted %q: %d bytes, valid UTF-8 %v", key, len(key), utf8.ValidString(key))
		}
		for seg := range strings.SplitSeq(key, Separator) {
			if len(seg) > MaxSegmentBytes {
				t.Fatalf("ValidateKey admitted %q with a segment of %d bytes", key, len(seg))
			}
		}
		if got, err := Compose(testTenant, longestCollection, key); err != nil || len(got) > MaxPathBytes {
			t.Fatalf("admitted key %q does not fit the longest collection: %d bytes, %v", key, len(got), err)
		}
	})
}
