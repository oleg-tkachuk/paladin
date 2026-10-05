package auth

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
)

// A capability-only request has no Authorization header at all, so the JWT gate
// has to step aside for it or it never reaches the interceptor that can
// authenticate it.
func TestInterceptorSkipTokensAndCapabilities_DefersACapability(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true, skipCapabilities: true}

	h := http.Header{}
	h.Set(HeaderCapability, "eyJ0eXAiOiJKV1QifQ.e30.sig")
	if !a.hasCapability(h) {
		t.Error("X-Paladin-Capability must be recognised")
	}

	h2 := http.Header{}
	h2.Set("Authorization", "Capability eyJ0eXAiOiJKV1QifQ.e30.sig")
	if !a.hasCapability(h2) {
		t.Error("Authorization: Capability must be recognised")
	}

	if a.hasCapability(http.Header{}) {
		t.Error("a request with neither must NOT be deferred — auth stays mandatory")
	}
}

// The plain gate keeps refusing capability-only requests, so enabling this is a
// per-plane decision rather than a global loosening.
func TestInterceptor_WithoutTheFlagDoesNotDeferCapabilities(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true}

	h := http.Header{}
	h.Set(HeaderCapability, "eyJ0eXAiOiJKV1QifQ.e30.sig")
	if a.hasCapability(h) {
		t.Error("a plane that did not opt in must not defer")
	}
}

// An API token in X-Paladin-API-Token alone — the header for a proxy that
// strips Authorization — must get past the JWT gate to the API-token
// interceptor behind it, on both paths. The gate used to read only
// Authorization, and refused it as "missing Authorization header".
func TestJWTGateDefersAnAPITokenInItsOwnHeader(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	tenant := uuid.New()
	tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	gate := InterceptorSkipTokensAndCapabilities(nil) // never reached: the gate steps aside
	pat := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}

	t.Run("unary", func(t *testing.T) {
		var seen context.Context
		final := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			seen = ctx
			return nil, nil
		}
		req := connect.NewRequest(&struct{}{})
		req.Header().Set(HeaderAPIToken, tok.Plaintext)
		if _, err := gate.WrapUnary(pat.WrapUnary(final))(context.Background(), req); err != nil {
			t.Fatalf("the chain refused the token: %v", err)
		}
		if got, ok := APITokenFromContext(seen); !ok || got.TenantID != tenant {
			t.Errorf("token on the context = %v, %v; want the verified one", got, ok)
		}
	})
	t.Run("stream", func(t *testing.T) {
		conn := newStreamConn()
		conn.header.Set(HeaderAPIToken, tok.Plaintext)
		var called bool
		var seen context.Context
		if err := gate.WrapStreamingHandler(pat.WrapStreamingHandler(streamNext(&called, &seen)))(context.Background(), conn); err != nil {
			t.Fatalf("the chain refused the token: %v", err)
		}
		if got, ok := APITokenFromContext(seen); !called || !ok || got.TenantID != tenant {
			t.Errorf("token on the context = %v, %v; want the verified one", got, ok)
		}
	})
}
