package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
)

// TestExtractAPIToken covers the header parsing branches: X-Paladin-API-Token
// wins, Authorization Bearer with `paladin_pat_` prefix is parsed, Bearer
// values without the prefix are skipped (so OIDC JWTs flow downstream),
// other schemes ignored.
func TestExtractAPIToken(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		xlegate string
		authz   string
		want    string
	}{
		"x-paladin wins":               {xlegate: "paladin_pat_AAA", authz: "Bearer paladin_pat_BBB", want: "paladin_pat_AAA"},
		"authz bearer with prefix":     {xlegate: "", authz: "Bearer paladin_pat_BBB", want: "paladin_pat_BBB"},
		"authz bearer non-pat ignored": {xlegate: "", authz: "Bearer some.jwt.value", want: ""},
		"authz scheme not bearer":      {xlegate: "", authz: "Basic paladin_pat_BBB", want: ""},
		"authz malformed":              {xlegate: "", authz: "Bearer", want: ""},
		"authz empty":                  {xlegate: "", authz: "", want: ""},
		"x-paladin without prefix":     {xlegate: "not-a-pat", authz: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := extractAPIToken(tc.xlegate, tc.authz)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAPITokenInterceptor_NilVerifier confirms passthrough when the
// subsystem is disabled — same shape as CapabilityInterceptor: a call
// carrying a token reaches the handler with no token stamped.
func TestAPITokenInterceptor_NilVerifier(t *testing.T) {
	t.Parallel()
	c := callProbe(context.Background(), []connect.ServerInterceptor{APITokenInterceptor(nil, planeData)},
		HeaderAPIToken, unknownPAT)
	if c.err != nil {
		t.Fatalf("expected a pass-through, got %v", c.err)
	}
	if _, ok := APITokenFromContext(c.handlerCtx); ok {
		t.Error("a disabled subsystem stamped a token on the context")
	}
}
