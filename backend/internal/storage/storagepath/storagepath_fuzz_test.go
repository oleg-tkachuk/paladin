package storagepath

import (
	"strings"
	"testing"
	"uuid"
)

// pathSep separates the segments of a bucket key.
const pathSep = "/"

// Whatever collection and key a caller names, the bucket key stays under the
// tenant's prefix, and no segment of it is one a filesystem-backed adapter or
// a prefix check would read as a step up or sideways.
//
// Compose is not injective: "a/./b", "a//b" and "a/b" share one bucket key,
// and so do collection "a/b" with key "c" and collection "a" with key "b/c".
// BACKLOG.md ("Object keys that alias one storage path") tracks that; this
// target pins only the containment.
func FuzzCompose(f *testing.F) {
	for _, c := range [][2]string{
		{"clean", "a/b/c"},
		{"../other", "../../x"},
		{"/leading", "./rel"},
		{"a//b", "../../etc/passwd"},
		{"team/docs", ".."},
		{"", ""},
	} {
		f.Add(c[0], c[1])
	}
	tenant := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	prefix := tenant.String() + pathSep
	f.Fuzz(func(t *testing.T, collection, key string) {
		got := Compose(tenant, collection, key)
		rest, ok := strings.CutPrefix(got, prefix)
		if !ok {
			t.Fatalf("Compose(%q, %q) = %q, outside %q", collection, key, got, prefix)
		}
		whole := sanitizeSegment(collection) != "" && sanitizeSegment(key) != ""
		for seg := range strings.SplitSeq(rest, pathSep) {
			if seg == "." || seg == ".." || (whole && seg == "") {
				t.Fatalf("Compose(%q, %q) = %q holds segment %q", collection, key, got, seg)
			}
		}
	})
}
