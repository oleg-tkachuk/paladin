package adapters

import (
	"strings"
	"testing"
	"uuid"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
)

// The page token a collection list mints resumes at the (tenant, collection)
// it was made from, whatever the collection's name holds: a tenant id is a
// UUID and never contains the separator, so the first one splits it.
func FuzzCollectionCursorRoundTrip(f *testing.F) {
	tenant := uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")
	for _, name := range []string{"docs", "team/docs", "a:b", ":", ""} {
		f.Add(tenant[:], name)
	}
	fallback := uuid.MustParse("3a823fd4-0b3d-4ce2-a280-93b8d75cc07b")
	f.Fuzz(func(t *testing.T, raw []byte, name string) {
		if len(raw) != len(uuid.UUID{}) {
			t.Skip("not a tenant id")
		}
		last := objectkey.Collection{TenantID: uuid.UUID(raw), Collection: name}
		tok := nextCollectionCursor([]objectkey.Collection{last}, 1)
		gotTenant, gotName := decodeCollectionCursor(tok, fallback)
		if gotTenant != last.TenantID || gotName != name {
			t.Fatalf("%q resumes at (%s, %q), want (%s, %q)", tok, gotTenant, gotName, last.TenantID, name)
		}
	})
}

// Any collection page token decodes without failing: one that names no
// tenant resumes within the listed one, as the tokens minted before the
// tenant was added did.
func FuzzDecodeCollectionCursor(f *testing.F) {
	for _, tok := range []string{"7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6:docs", "docs", "not-a-uuid:docs", ":", ""} {
		f.Add(tok)
	}
	fallback := uuid.MustParse("3a823fd4-0b3d-4ce2-a280-93b8d75cc07b")
	f.Fuzz(func(t *testing.T, tok string) {
		tenant, name := decodeCollectionCursor(tok, fallback)
		if name == tok && tenant == fallback {
			return
		}
		head, ok := strings.CutSuffix(tok, collectionCursorSep+name)
		named, err := uuid.Parse(head)
		if !ok || err != nil || named != tenant {
			t.Fatalf("%q resumes at (%s, %q): neither the listed tenant nor the one it names", tok, tenant, name)
		}
	})
}
