package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sampleCapability is shaped like a capability token; these tests only parse
// headers, so it is never verified.
const sampleCapability = "eyJ0eXAiOiJKV1QifQ.e30.sig"

// capabilityScheme is the Authorization scheme a capability may ride in.
const capabilityScheme = "Capability "

// A capability-only request has no Authorization header at all, so the JWT gate
// has to step aside for it or it never reaches the interceptor that can
// authenticate it.
func TestInterceptorSkipTokensAndCapabilities_DefersACapability(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true, skipCapabilities: true}

	h := &connect.Header{}
	h.Set(HeaderCapability, sampleCapability)
	if !a.hasCapability(h) {
		t.Error("X-Paladin-Capability must be recognised")
	}

	h2 := &connect.Header{}
	h2.Set(paladin.HeaderAuthorization, capabilityScheme+sampleCapability)
	if !a.hasCapability(h2) {
		t.Error("Authorization: Capability must be recognised")
	}

	if a.hasCapability(&connect.Header{}) {
		t.Error("a request with neither must NOT be deferred — auth stays mandatory")
	}
}

// The plain gate keeps refusing capability-only requests, so enabling this is a
// per-plane decision rather than a global loosening.
func TestInterceptor_WithoutTheFlagDoesNotDeferCapabilities(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true}

	h := &connect.Header{}
	h.Set(HeaderCapability, sampleCapability)
	if a.hasCapability(h) {
		t.Error("a plane that did not opt in must not defer")
	}
}

// An API token in X-Paladin-API-Token alone — the header for a proxy that
// strips Authorization — must get past the JWT gate to the API-token
// interceptor behind it. The gate used to read only Authorization, and
// refused it as "missing Authorization header". One gate function serves
// unary and streaming calls alike, so one call shape covers both.
func TestJWTGateDefersAnAPITokenInItsOwnHeader(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	tenant := uuid.New()
	tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	gate := InterceptorSkipTokensAndCapabilities(nil) // never reached: the gate steps aside
	pat := &apiTokenInterceptor{verifier: f.verifier, audience: planeData}

	c := callProbe(context.Background(), []connect.ServerInterceptor{gate, pat.intercept},
		HeaderAPIToken, tok.Plaintext)
	if c.err != nil {
		t.Fatalf("the chain refused the token: %v", c.err)
	}
	if got, ok := APITokenFromContext(c.handlerCtx); !ok || got.TenantID != tenant {
		t.Errorf("token on the context = %v, %v; want the verified one", got, ok)
	}
}
