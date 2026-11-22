package apitokenh

import (
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// An API token's name, on any input: refused, or a tenant and a token id that
// TokenName prints back to a name parsing to the same pair. The tenant is the
// one RLS scopes the token's row by, so a name must not parse to a tenant its
// text does not lead with.
func FuzzParseTokenName(f *testing.F) {
	tenant := uuid.MustParse("3a823fd4-0b3d-4ce2-a280-93b8d75cc07b")
	token := uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")
	for _, s := range []string{
		TokenName(tenant, token),
		"tenants/" + tenant.String() + "/apiTokens/",
		"tenants//apiTokens/" + token.String(),
		"tenants/" + tenant.String() + "/apiTokens/" + token.String() + "/apiTokens/" + token.String(),
		"tenants/{" + tenant.String() + "}/apiTokens/" + token.String(),
		"apiTokens/" + token.String(),
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		tenantID, tokenID, err := parseTokenName(name)
		if err != nil {
			if tenantID != uuid.Nil() || tokenID != uuid.Nil() {
				t.Fatalf("%q: refused but returned (%s, %s)", name, tenantID, tokenID)
			}
			return
		}
		if named, ok := apiutil.TenantInResourceName(name); !ok || named != tenantID {
			t.Fatalf("%q parses to tenant %s, its text names (%s, %v)", name, tenantID, named, ok)
		}
		again, againID, err := parseTokenName(TokenName(tenantID, tokenID))
		if err != nil || again != tenantID || againID != tokenID {
			t.Fatalf("%q: %q parses back to (%s, %s, %v)", name, TokenName(tenantID, tokenID), again, againID, err)
		}
	})
}
