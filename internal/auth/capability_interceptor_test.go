package auth

import (
	"testing"
)

// TestExtractCapabilityToken covers the header parsing branches:
// X-PALADIN-Capability wins over Authorization, Authorization with the
// "Capability" scheme is parsed, other schemes are ignored, malformed
// values return empty.
func TestExtractCapabilityToken(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		xocp  string
		authz string
		want  string
	}{
		"x-paladin wins":           {xocp: "tok-a", authz: "Capability tok-b", want: "tok-a"},
		"authz capability":     {xocp: "", authz: "Capability tok-b", want: "tok-b"},
		"authz lowercase":      {xocp: "", authz: "capability tok-b", want: "tok-b"},
		"authz bearer ignored": {xocp: "", authz: "Bearer tok-b", want: ""},
		"authz malformed":      {xocp: "", authz: "Capability", want: ""},
		"authz empty":          {xocp: "", authz: "", want: ""},
		"x-paladin whitespace":     {xocp: "  tok-c  ", authz: "", want: "tok-c"},
		"both empty":           {xocp: "   ", authz: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := extractCapabilityToken(tc.xocp, tc.authz)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCapabilityInterceptor_NilVerifier confirms the interceptor is a
// pass-through when the capability subsystem is disabled — the
// returned value implements connect.Interceptor and forwards calls
// unchanged.
func TestCapabilityInterceptor_NilVerifier(t *testing.T) {
	t.Parallel()
	i := CapabilityInterceptor(nil, "data", nil, "")
	if i == nil {
		t.Fatalf("nil interceptor returned")
	}
	// passthroughInterceptor.WrapUnary returns its argument verbatim;
	// confirming via reflection-free identity is enough — the type
	// switch works because we return the concrete passthroughInterceptor.
	if _, ok := i.(passthroughInterceptor); !ok {
		t.Fatalf("expected passthroughInterceptor, got %T", i)
	}
}
