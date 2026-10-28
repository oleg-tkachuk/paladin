package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
)

// The capability issuer of these tests, and the delegation issuer it makes.
const capabilityIssuerName = "paladin-capability"

var delegationIssuer = DelegationIssuerName(capabilityIssuerName)

// relay is the MCP server's client of the exchange, which holds every
// delegated capability these tests make.
const relay = "paladin-mcp-server"

// delegatedCapability is one the MCP server exchanged for user's token,
// under the scopes the user consented to.
func delegatedCapability(t *testing.T, user DelegatingUser, id uuid.UUID, scopes ...string) *limes.Capability {
	t.Helper()
	ops, err := DelegatedOps(scopes)
	if err != nil {
		t.Fatal(err)
	}
	return &limes.Capability{
		ID: uuid.New(), Issuer: delegationIssuer, ClientID: "agent",
		Subject:    limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: user.TenantID, Subject: relay},
		OnBehalfOf: &limes.Principal{Type: limes.PrincipalTypeUser, TenantID: user.TenantID, Subject: id.String()},
		Caveats:    limes.Caveats{Ops: ops},
		ExpiresAt:  time.Now().Add(time.Minute).Truncate(time.Second),
	}
}

// directory answers for a fixed set of users.
func directory(users map[uuid.UUID]DelegatingUser, err error) DelegatingUserLookup {
	return func(_ context.Context, id uuid.UUID) (DelegatingUser, error) {
		if err != nil {
			return DelegatingUser{}, err
		}
		u, ok := users[id]
		if !ok {
			return DelegatingUser{}, ErrDelegatingUserGone
		}
		return u, nil
	}
}

func delegatingInterceptor(audience string, lookup DelegatingUserLookup) *capabilityInterceptor {
	i := &capabilityInterceptor{audience: audience}
	WithDelegation(delegationIssuer, lookup)(i)
	return i
}

// On every plane, the additive admin plane included, a capability the MCP
// server exchanged acts for its user: the user's tenant, roles and resource
// scopes, the agent's grant, and the MCP server as the actor.
func TestDelegatedCapabilityActsForTheUser(t *testing.T) {
	id := uuid.New()
	user := DelegatingUser{TenantID: uuid.New(), Roles: []string{"tenant.admin"}, Scopes: []Scope{{Type: ScopeTenant}}}
	i := delegatingInterceptor(TokenPlaneAdmin, directory(map[uuid.UUID]DelegatingUser{id: user}, nil))
	c := delegatedCapability(t, user, id, OAuthScopeRead, OAuthScopeWrite)

	ctx, err := i.withPrincipal(context.Background(), c)
	if err != nil {
		t.Fatalf("withPrincipal: %v", err)
	}
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		t.Fatalf("no principal: %v", err)
	}
	if p.Subject != id.String() || p.TenantID != user.TenantID || p.Kind != PrincipalKindUser {
		t.Errorf("principal %+v, want user %s in %s", p, id, user.TenantID)
	}
	if !slices.Equal(p.Roles, user.Roles) || !slices.Equal(p.Scopes, user.Scopes) {
		t.Errorf("roles %v and scopes %v, want the user's %v and %v", p.Roles, p.Scopes, user.Roles, user.Scopes)
	}
	if p.Audience != AudienceAdmin || !slices.Equal(p.Audiences, []string{AudienceAdmin}) {
		t.Errorf("audience %q %v, want the plane's", p.Audience, p.Audiences)
	}
	if p.OAuthClientID != c.ClientID || !p.MayWrite() || p.Actor != relay {
		t.Errorf("grant %q %v, actor %q; want the agent's write grant relayed by %s", p.OAuthClientID, p.OAuthScopes, p.Actor, relay)
	}
	if !p.ExpiresAt.Equal(c.ExpiresAt) {
		t.Errorf("expires %v, want the capability's %v", p.ExpiresAt, c.ExpiresAt)
	}
}

// A grant without paladin.write reads; the write interceptor refuses it the
// rest, as it would the user's own OAuth token.
func TestDelegatedCapabilityKeepsAReadOnlyGrant(t *testing.T) {
	id := uuid.New()
	user := DelegatingUser{TenantID: uuid.New()}
	i := delegatingInterceptor(TokenPlaneData, directory(map[uuid.UUID]DelegatingUser{id: user}, nil))
	ctx, err := i.withPrincipal(context.Background(), delegatedCapability(t, user, id, OAuthScopeRead))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := PrincipalFromContext(ctx); p.MayWrite() {
		t.Errorf("a read-only grant may write: %v", p.OAuthScopes)
	}
}

// A user who may no longer act is refused, and a directory that fails is
// the server's failure, not the caller's.
func TestDelegatedCapabilityRefusals(t *testing.T) {
	id := uuid.New()
	user := DelegatingUser{TenantID: uuid.New()}
	c := delegatedCapability(t, user, id, OAuthScopeRead)
	disabled := user
	disabled.Disabled = true
	moved := user
	moved.TenantID = uuid.New()
	down := errors.New("pool closed")
	noUser := *c
	noUser.OnBehalfOf = nil
	notAUser := *c
	notAUser.OnBehalfOf = &limes.Principal{Type: limes.PrincipalTypeUser, TenantID: user.TenantID, Subject: "alice"}

	for _, tc := range []struct {
		name   string
		lookup DelegatingUserLookup
		cap    *limes.Capability
		want   connect.Code
	}{
		{"a user who is gone", directory(nil, nil), c, connect.CodePermissionDenied},
		{"a disabled user", directory(map[uuid.UUID]DelegatingUser{id: disabled}, nil), c, connect.CodePermissionDenied},
		{"a user now in another tenant", directory(map[uuid.UUID]DelegatingUser{id: moved}, nil), c, connect.CodePermissionDenied},
		{"no user named", directory(map[uuid.UUID]DelegatingUser{id: user}, nil), &noUser, connect.CodePermissionDenied},
		{"a user that is no id", directory(map[uuid.UUID]DelegatingUser{id: user}, nil), &notAUser, connect.CodePermissionDenied},
		{"a directory that fails", directory(nil, down), c, connect.CodeUnavailable},
	} {
		i := delegatingInterceptor(TokenPlaneData, tc.lookup)
		ctx, err := i.withPrincipal(context.Background(), tc.cap)
		if connect.CodeOf(err) != tc.want {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if _, perr := PrincipalFromContext(ctx); perr == nil {
			t.Errorf("%s: a principal was established", tc.name)
		}
	}
}

// Only the delegation issuer's capabilities act for a user. One from the
// capability issuer naming a user keeps ADR-0010: on the additive admin
// plane it establishes nothing, and on the data plane no roles.
func TestOnlyTheDelegationIssuerActsForAUser(t *testing.T) {
	id := uuid.New()
	user := DelegatingUser{TenantID: uuid.New(), Roles: []string{"platform.admin"}}
	lookup := directory(map[uuid.UUID]DelegatingUser{id: user}, nil)
	c := delegatedCapability(t, user, id, OAuthScopeRead)
	c.Issuer = capabilityIssuerName

	ctx, err := delegatingInterceptor(TokenPlaneAdmin, lookup).withPrincipal(context.Background(), c)
	if _, perr := PrincipalFromContext(ctx); err != nil || perr == nil {
		t.Errorf("admin plane: err %v, principal established %v; want neither", err, perr == nil)
	}

	data := delegatingInterceptor(TokenPlaneData, lookup)
	data.establishPrincipal = true
	ctx, err = data.withPrincipal(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := PrincipalFromContext(ctx); p.Kind != PrincipalKindCapability || len(p.Roles) != 0 {
		t.Errorf("data plane: principal %+v, want a roleless capability", p)
	}
}

// A delegated capability beside a JWT or API token stays additive, as every
// capability does.
func TestDelegatedCapabilityLeavesAnExistingPrincipal(t *testing.T) {
	id := uuid.New()
	user := DelegatingUser{TenantID: uuid.New(), Roles: []string{"platform.admin"}}
	original := &Principal{TenantID: uuid.New(), Subject: "someone", Kind: PrincipalKindUser}
	i := delegatingInterceptor(TokenPlaneData, directory(map[uuid.UUID]DelegatingUser{id: user}, nil))
	ctx, err := i.withPrincipal(WithPrincipal(context.Background(), original), delegatedCapability(t, user, id, OAuthScopeRead))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := PrincipalFromContext(ctx); p != original {
		t.Errorf("principal replaced by %+v", p)
	}
}
