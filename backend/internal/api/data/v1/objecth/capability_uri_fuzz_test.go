package objecth

import (
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// fuzzTenant is the tenant every fuzzed URI is built for.
var fuzzTenant = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")

// A URI names exactly one (collection, key): it parses back to its input,
// two different inputs never share one, and a grant on a collection reaches
// another collection's object only when that is the same collection.
func FuzzCapabilityObjectURI(f *testing.F) {
	for _, c := range [][4]string{
		{"a", "b/c", "a/b", "c"},
		{"a", "x", "ab", "x"},
		{"invoices/2026/q1", "r.pdf", "invoices", "2026/q1/r.pdf"},
		{"docs", "a.pdf", "docs", "a.pdf"},
	} {
		f.Add(c[0], c[1], c[2], c[3])
	}
	f.Fuzz(func(t *testing.T, c1, k1, c2, k2 string) {
		if storagepath.ValidateCollection(c1) != nil || storagepath.ValidateKey(k1) != nil ||
			storagepath.ValidateCollection(c2) != nil || storagepath.ValidateKey(k2) != nil {
			return
		}
		u1, u2 := CapabilityObjectURI(fuzzTenant, c1, k1), CapabilityObjectURI(fuzzTenant, c2, k2)
		tenant, collection, key, ok := parseCapabilityObjectURI(u1)
		if !ok || tenant != fuzzTenant || collection != c1 || key != k1 {
			t.Fatalf("parse(%q) = %v %q %q %v, want (%q, %q)", u1, tenant, collection, key, ok, c1, k1)
		}
		if u1 == u2 && (c1 != c2 || k1 != k2) {
			t.Fatalf("(%q, %q) and (%q, %q) share %q", c1, k1, c2, k2, u1)
		}
		if limes.MatchResource(capabilityCollectionURI(fuzzTenant, c1), u2) && c1 != c2 {
			t.Fatalf("a grant on collection %q reaches %q of collection %q", c1, k2, c2)
		}
	})
}
