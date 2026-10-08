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

// The constructors return interceptor funcs, so these tests drive each one
// through a real call with a real verifier and read the identity the handler
// ran under, rather than inspecting what a constructor built.

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// roleIssuer is a role only a platform admin can grant a token.
const roleIssuer = "platform.capability-issuer"

// establishes reports whether a call carrying token through interceptor
// reaches the handler with a principal.
func establishes(t *testing.T, interceptor connect.ServerInterceptor, token string) bool {
	t.Helper()
	c := callProbe(context.Background(), []connect.ServerInterceptor{interceptor},
		paladin.HeaderAuthorization, bearerPrefix+token)
	if c.err != nil {
		t.Fatalf("call refused: %v", c.err)
	}
	_, perr := PrincipalFromContext(c.handlerCtx)
	return perr == nil
}

func TestAPITokenConstructors_SetTheirEstablishmentPolicy(t *testing.T) {
	f := newTokenFixture(t)
	planes := []string{planeData, planeAdmin}
	roleless := f.issue(t, api_token.IssueRequest{Audience: planes}).Plaintext
	withRole := f.issue(t, api_token.IssueRequest{Audience: planes, Roles: []string{roleIssuer}}).Plaintext

	t.Run("data plane authenticates any valid token", func(t *testing.T) {
		i := APITokenAuthInterceptor(f.verifier, nil, planeData)
		if !establishes(t, i, roleless) {
			t.Error("a roleless service token was not authenticated on the data plane; every PAT would be refused")
		}
		if !establishes(t, i, withRole) {
			t.Error("a role-bearing token was not authenticated on the data plane")
		}
	})

	t.Run("admin plane authenticates only a role-bearing token", func(t *testing.T) {
		i := APITokenRoleAuthInterceptor(f.verifier, nil, planeAdmin)
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
		data := APITokenAuthInterceptor(f.verifier, nil, planeData)
		admin := APITokenRoleAuthInterceptor(f.verifier, nil, planeAdmin)
		if establishes(t, data, roleless) == establishes(t, admin, roleless) {
			t.Error("both planes reached the same verdict on a roleless token; " +
				"the role requirement is the only difference between these constructors")
		}
	})
}

// A nil verifier means the subsystem is off, and both constructors must then
// return the pass-through rather than an interceptor that would dereference
// it on the first request carrying a token: a call with a PAT reaches the
// handler with neither a token nor a principal.
func TestAPITokenConstructors_NilVerifierIsAPassThrough(t *testing.T) {
	for name, got := range map[string]connect.ServerInterceptor{
		"data":  APITokenAuthInterceptor(nil, nil, planeData),
		"admin": APITokenRoleAuthInterceptor(nil, nil, planeAdmin),
	} {
		t.Run(name, func(t *testing.T) {
			if got == nil {
				t.Fatal("constructor returned nil")
			}
			c := callProbe(context.Background(), []connect.ServerInterceptor{got},
				paladin.HeaderAuthorization, bearerPrefix+unknownPAT)
			if c.err != nil {
				t.Fatalf("with no verifier a token-bearing call was refused: %v", c.err)
			}
			if _, ok := APITokenFromContext(c.handlerCtx); ok {
				t.Error("with no verifier a token was stamped on the context")
			}
			if _, err := PrincipalFromContext(c.handlerCtx); err == nil {
				t.Error("with no verifier a principal was established")
			}
		})
	}

	// And a configured verifier must NOT return the pass-through, or tokens
	// stop being verified at all: a token it never issued is refused.
	f := newTokenFixture(t)
	for name, got := range map[string]connect.ServerInterceptor{
		"data":  APITokenAuthInterceptor(f.verifier, nil, planeData),
		"admin": APITokenRoleAuthInterceptor(f.verifier, nil, planeAdmin),
	} {
		t.Run(name+" configured", func(t *testing.T) {
			c := callProbe(context.Background(), []connect.ServerInterceptor{got},
				paladin.HeaderAuthorization, bearerPrefix+unknownPAT)
			if connect.CodeOf(c.err) != connect.CodeUnauthenticated {
				t.Errorf("an unknown token got %v; API tokens would never be verified", c.err)
			}
		})
	}
}

// The establishment is additive: a JWT that already authenticated wins, so a
// token presented alongside one cannot substitute its own identity.
func TestAPITokenConstructors_DoNotDisplaceAnExistingPrincipal(t *testing.T) {
	f := newTokenFixture(t)
	jwtPrincipal := &Principal{Subject: "human@acme", TenantID: uuid.New()}
	tok := f.issue(t, api_token.IssueRequest{
		TenantID: uuid.New(), // a DIFFERENT tenant
		Audience: []string{planeData, planeAdmin},
		Roles:    []string{roleIssuer},
	})

	for name, ctor := range map[string]func() connect.ServerInterceptor{
		"data":  func() connect.ServerInterceptor { return APITokenAuthInterceptor(f.verifier, nil, planeData) },
		"admin": func() connect.ServerInterceptor { return APITokenRoleAuthInterceptor(f.verifier, nil, planeAdmin) },
	} {
		t.Run(name, func(t *testing.T) {
			c := callProbe(WithPrincipal(context.Background(), jwtPrincipal), []connect.ServerInterceptor{ctor()},
				paladin.HeaderAuthorization, bearerPrefix+tok.Plaintext)
			if c.err != nil {
				t.Fatalf("call refused: %v", c.err)
			}
			p, perr := PrincipalFromContext(c.handlerCtx)
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
