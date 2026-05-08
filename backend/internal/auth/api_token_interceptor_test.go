package auth

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
)

// TestExtractAPIToken covers the header parsing branches: X-PALADIN-API-Token
// wins, Authorization Bearer with `paladin_pat_` prefix is parsed, Bearer
// values without the prefix are skipped (so OIDC JWTs flow downstream),
// other schemes ignored.
func TestExtractAPIToken(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		xocp  string
		authz string
		want  string
	}{
		"x-paladin wins":                   {xocp: "paladin_pat_AAA", authz: "Bearer paladin_pat_BBB", want: "paladin_pat_AAA"},
		"authz bearer with prefix":     {xocp: "", authz: "Bearer paladin_pat_BBB", want: "paladin_pat_BBB"},
		"authz bearer non-pat ignored": {xocp: "", authz: "Bearer some.jwt.value", want: ""},
		"authz scheme not bearer":      {xocp: "", authz: "Basic paladin_pat_BBB", want: ""},
		"authz malformed":              {xocp: "", authz: "Bearer", want: ""},
		"authz empty":                  {xocp: "", authz: "", want: ""},
		"x-paladin without prefix":         {xocp: "not-a-pat", authz: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := extractAPIToken(tc.xocp, tc.authz)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAPITokenInterceptor_NilVerifier confirms passthrough when the
// subsystem is disabled — same shape as CapabilityInterceptor.
func TestAPITokenInterceptor_NilVerifier(t *testing.T) {
	t.Parallel()
	i := APITokenInterceptor(nil, "data")
	if _, ok := i.(passthroughInterceptor); !ok {
		t.Fatalf("expected passthroughInterceptor, got %T", i)
	}
}

// silence lint about unused import — api_token is referenced by the
// extracted symbol prefix in extractAPIToken's contract.
var _ = api_token.TokenPrefix
