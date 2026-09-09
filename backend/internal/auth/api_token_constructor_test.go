package auth

// The two API-token interceptors differ by exactly one field, and that field
// is the whole of the admin plane's posture toward service tokens.
//
// withTokenIdentity is tested for every combination of those flags — but by
// constructing the struct directly, which is how both constructors escaped
// scrutiny entirely. Swap their bodies and every existing test still passes,
// while an ordinary roleless service token starts authenticating on the admin
// plane: the comment above APITokenRoleAuthInterceptor spells out the
// consequence, that RPCs gated on tenant alone "would newly admit it".
//
// The other direction is an outage rather than a hole — a data plane that
// stops establishing principals refuses every PAT — but it is just as silent
// at the point of change.

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
)

func asTokenInterceptor(t *testing.T, i connect.Interceptor) *apiTokenInterceptor {
	t.Helper()
	ti, ok := i.(*apiTokenInterceptor)
	if !ok {
		t.Fatalf("constructor returned %T, want *apiTokenInterceptor", i)
	}
	return ti
}

func establishes(t *testing.T, i *apiTokenInterceptor, tok *api_token.Token) bool {
	t.Helper()
	ctx, err := i.withTokenIdentity(context.Background(), tok)
	if err != nil {
		t.Fatalf("withTokenIdentity: %v", err)
	}
	_, perr := PrincipalFromContext(ctx)
	return perr == nil
}

func TestAPITokenConstructors_SetTheirEstablishmentPolicy(t *testing.T) {
	roleless := &api_token.Token{ID: uuid.New(), TenantID: uuid.New()}
	withRole := &api_token.Token{
		ID: uuid.New(), TenantID: uuid.New(),
		Roles: []string{"platform.capability-issuer"},
	}
	verifier := &api_token.Verifier{} // never called; only its non-nil-ness matters here

	t.Run("data plane authenticates any valid token", func(t *testing.T) {
		i := asTokenInterceptor(t, APITokenAuthInterceptor(verifier, nil, "data"))
		if !establishes(t, i, roleless) {
			t.Error("a roleless service token was not authenticated on the data plane; every PAT would be refused")
		}
		if !establishes(t, i, withRole) {
			t.Error("a role-bearing token was not authenticated on the data plane")
		}
	})

	t.Run("admin plane authenticates only a role-bearing token", func(t *testing.T) {
		i := asTokenInterceptor(t, APITokenRoleAuthInterceptor(verifier, nil, "admin"))
		if establishes(t, i, roleless) {
			t.Error("a roleless service token authenticated on the admin plane; " +
				"RPCs gated on tenant alone would newly admit it")
		}
		if !establishes(t, i, withRole) {
			t.Error("a role-bearing token was refused on the admin plane; only a platform admin can mint one")
		}
	})

	// The distinguishing case, stated on its own: the two constructors must
	// not agree about a roleless token. If they ever do, one of them is wrong
	// whichever way it went.
	t.Run("the two planes disagree about a roleless token", func(t *testing.T) {
		data := asTokenInterceptor(t, APITokenAuthInterceptor(verifier, nil, "data"))
		admin := asTokenInterceptor(t, APITokenRoleAuthInterceptor(verifier, nil, "admin"))
		if establishes(t, data, roleless) == establishes(t, admin, roleless) {
			t.Error("both planes reached the same verdict on a roleless token; " +
				"the role requirement is the only difference between these constructors")
		}
	})
}

// A nil verifier means the subsystem is off, and both constructors must then
// return the pass-through rather than an interceptor that would dereference
// it on the first request carrying a token.
func TestAPITokenConstructors_NilVerifierIsAPassThrough(t *testing.T) {
	for name, got := range map[string]connect.Interceptor{
		"data":  APITokenAuthInterceptor(nil, nil, "data"),
		"admin": APITokenRoleAuthInterceptor(nil, nil, "admin"),
	} {
		t.Run(name, func(t *testing.T) {
			if got == nil {
				t.Fatal("constructor returned nil")
			}
			if _, ok := got.(passthroughInterceptor); !ok {
				t.Errorf("with no verifier the constructor returned %T; a token-bearing "+
					"request would reach Verify on a nil receiver", got)
			}
		})
	}

	// And a configured verifier must NOT return the pass-through, or tokens
	// stop being verified at all.
	for name, got := range map[string]connect.Interceptor{
		"data":  APITokenAuthInterceptor(&api_token.Verifier{}, nil, "data"),
		"admin": APITokenRoleAuthInterceptor(&api_token.Verifier{}, nil, "admin"),
	} {
		t.Run(name+" configured", func(t *testing.T) {
			if _, ok := got.(passthroughInterceptor); ok {
				t.Error("a configured verifier produced a pass-through; API tokens would never be verified")
			}
		})
	}
}

// The establishment is additive: a JWT that already authenticated wins, so a
// token presented alongside one cannot substitute its own identity.
func TestAPITokenConstructors_DoNotDisplaceAnExistingPrincipal(t *testing.T) {
	jwtPrincipal := &Principal{Subject: "human@acme", TenantID: uuid.New()}
	tok := &api_token.Token{
		ID: uuid.New(), TenantID: uuid.New(), // a DIFFERENT tenant
		Roles: []string{"platform.capability-issuer"},
	}

	for name, ctor := range map[string]func() connect.Interceptor{
		"data":  func() connect.Interceptor { return APITokenAuthInterceptor(&api_token.Verifier{}, nil, "data") },
		"admin": func() connect.Interceptor { return APITokenRoleAuthInterceptor(&api_token.Verifier{}, nil, "admin") },
	} {
		t.Run(name, func(t *testing.T) {
			i := asTokenInterceptor(t, ctor())
			ctx, err := i.withTokenIdentity(WithPrincipal(context.Background(), jwtPrincipal), tok)
			if err != nil {
				t.Fatalf("withTokenIdentity: %v", err)
			}
			p, perr := PrincipalFromContext(ctx)
			if perr != nil {
				t.Fatalf("the existing principal was lost: %v", perr)
			}
			if p.Subject != jwtPrincipal.Subject || p.TenantID != jwtPrincipal.TenantID {
				t.Errorf("principal = %s/%v, want the JWT's %s/%v — a token must not displace it",
					p.Subject, p.TenantID, jwtPrincipal.Subject, jwtPrincipal.TenantID)
			}
		})
	}
}
