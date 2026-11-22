package paladin_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Separators a parsed id must never carry: a tenant, object or version id is
// one segment of the name, so a '/' in one would let its text name one
// resource and its fields address another.
const nameSegmentSep = "/"

// seedNames adds every input of the shared name table, so `go test` runs the
// cases both SDKs agree on as the fuzz targets' regression corpus.
func seedNames(f *testing.F) {
	f.Helper()
	raw, err := os.ReadFile(sharedCases)
	if err != nil {
		f.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		f.Fatal(err)
	}
	for kind, msg := range all {
		if strings.HasPrefix(kind, "_") {
			continue
		}
		var cases []nameCase
		if err := json.Unmarshal(msg, &cases); err != nil {
			f.Fatalf("%s: %v", kind, err)
		}
		for _, tc := range cases {
			f.Add(tc.In)
		}
	}
	for _, s := range []string{
		"storageBackends/primary/buckets/paladin-shared",
		"storageBackends//buckets/b",
		"storageBackends/p/buckets/b/extra",
		"tenants/{0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00}/collections/docs",
		"tenants/urn:uuid:0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00/collections/docs",
	} {
		f.Add(s)
	}
}

// canonicalID reports whether s is a UUID in the form the server prints.
func canonicalID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s
}

// refusedAsInvalidName fails t unless err is the parsers' one refusal.
func refusedAsInvalidName(t *testing.T, kind, in string, err error) {
	t.Helper()
	if !errors.Is(err, paladin.ErrInvalidName) {
		t.Fatalf("%s %q: err = %v, want ErrInvalidName", kind, in, err)
	}
}

// Every resource-name parser, on any input: it refuses with ErrInvalidName or
// returns fields whose ids are canonical single segments, and what it prints
// parses back to the same fields.
func FuzzParseResourceName(f *testing.F) {
	seedNames(f)
	f.Fuzz(func(t *testing.T, in string) {
		if n, err := paladin.ParseTenantName(in); err != nil {
			refusedAsInvalidName(t, "tenant", in, err)
		} else {
			if n.Tenant == "" || strings.Contains(n.Tenant, nameSegmentSep) {
				t.Fatalf("tenant %q: tenant %q is not one segment", in, n.Tenant)
			}
			if again, err := paladin.ParseTenantName(n.String()); err != nil || again != n {
				t.Fatalf("tenant %q: %q parses back to (%+v, %v)", in, n.String(), again, err)
			}
		}

		if n, err := paladin.ParseCollectionName(in); err != nil {
			refusedAsInvalidName(t, "collection", in, err)
		} else {
			checkCollection(t, in, n)
		}

		if n, err := paladin.ParseObjectName(in); err != nil {
			refusedAsInvalidName(t, "object", in, err)
		} else {
			checkCollection(t, in, n.CollectionName)
			if !canonicalID(n.Object) {
				t.Fatalf("object %q: object id %q is not a canonical UUID", in, n.Object)
			}
			if again, err := paladin.ParseObjectName(n.String()); err != nil || again != n {
				t.Fatalf("object %q: %q parses back to (%+v, %v)", in, n.String(), again, err)
			}
		}

		if n, err := paladin.ParseObjectVersionName(in); err != nil {
			refusedAsInvalidName(t, "version", in, err)
		} else {
			checkCollection(t, in, n.CollectionName)
			if !canonicalID(n.Object) || !canonicalID(n.Version) {
				t.Fatalf("version %q: ids (%q, %q) are not canonical UUIDs", in, n.Object, n.Version)
			}
			if again, err := paladin.ParseObjectVersionName(n.String()); err != nil || again != n {
				t.Fatalf("version %q: %q parses back to (%+v, %v)", in, n.String(), again, err)
			}
		}

		if n, err := paladin.ParseBucketName(in); err != nil {
			refusedAsInvalidName(t, "bucket", in, err)
		} else {
			for _, seg := range []string{n.Backend, n.Bucket} {
				if seg == "" || strings.Contains(seg, nameSegmentSep) {
					t.Fatalf("bucket %q: segment %q is not one segment", in, seg)
				}
			}
			if n.String() != in {
				t.Fatalf("bucket %q prints as %q", in, n.String())
			}
		}
	})
}

// checkCollection holds what every name under a collection shares: a
// canonical tenant id, a collection that is not empty, and a print that parses
// back to the same.
func checkCollection(t *testing.T, in string, n paladin.CollectionName) {
	t.Helper()
	if !canonicalID(n.Tenant) {
		t.Fatalf("%q: tenant %q is not a canonical UUID", in, n.Tenant)
	}
	if n.Collection == "" {
		t.Fatalf("%q: empty collection", in)
	}
	if again, err := paladin.ParseCollectionName(n.String()); err != nil || again != n {
		t.Fatalf("%q: collection %q parses back to (%+v, %v)", in, n.String(), again, err)
	}
}

// An object URI, on any input: refused with ErrInvalidName, or a canonical
// tenant, a collection and a key that are not empty, and a print that parses
// back to the same — the escaping keeps a '/' in either from splitting it.
func FuzzParseObjectURI(f *testing.F) {
	seedNames(f)
	for _, s := range []string{
		"paladin://tenants/0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00/collections/team%2Fdocs/keys/a%2Fb.txt",
		"paladin://tenants/0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00/collections/c/keys/%zz",
		"paladin://tenants/0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00/collections/c/keys/a+b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		u, err := paladin.ParseObjectURI(in)
		if err != nil {
			refusedAsInvalidName(t, "uri", in, err)
			return
		}
		if !canonicalID(u.Collection.Tenant) || u.Collection.Collection == "" || u.Key == "" {
			t.Fatalf("uri %q parsed to %+v", in, u)
		}
		again, err := paladin.ParseObjectURI(u.String())
		if err != nil || again != u {
			t.Fatalf("uri %q: %q parses back to (%+v, %v)", in, u.String(), again, err)
		}
	})
}
