package tenanth

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// createCodes is what CreateTenant answers a malformed tenant with: a slug,
// default binding or storage layout it refuses.
var createCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever slug, display name and storage a create names, CreateTenant either
// creates the tenant — with a default policy that compiles, since the slug is
// written into it — or refuses it as the caller's mistake, in words that name
// nothing of the server.
func FuzzCreateTenant(f *testing.F) {
	for _, c := range [][5]string{
		{"acme", "", "", "", ""},
		{"acme", " Acme ", "b1", "bk1", ""},
		{"AB", "", "", "", ""},
		{"acme", "", "b1", "", ""},
		{"acme", "", "", "", StorageLayoutDedicated},
		{"acme", "", "b1", "bk1", StorageLayoutDedicated},
		{"acme", "", "", "", "weird"},
		{"a\"b", "x\x00", " ", "\t", ""},
		{"acme-2026", "Acme \u202e", "primary", "acme-data", "shared"},
	} {
		f.Add(c[0], c[1], c[2], c[3], c[4])
	}
	f.Fuzz(func(t *testing.T, slug, displayName, backend, bucket, layout string) {
		repo := &fakeRepo{}
		_, err := NewHandler(repo, allow()).CreateTenant(adminCtx(uuid.New()), CreateTenantArgs{
			Slug: slug, DisplayName: displayName,
			DefaultBackendID: backend, DefaultBucketName: bucket, StorageLayout: layout,
		})
		fuzzassert.Refusal(t, err, createCodes, slug, displayName, backend, bucket, layout)
		if err != nil {
			return
		}
		if verr := cedar.Validate(repo.lastCreateTx.InheritedCedarPolicy); verr != nil {
			t.Fatalf("slug %q seeds a default policy that does not compile: %v", slug, verr)
		}
	})
}
