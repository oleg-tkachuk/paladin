package delegation

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// usersByID is a user directory holding a fixed set of users.
type usersByID struct {
	users map[uuid.UUID]authstore.User
	err   error
}

func (u usersByID) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if u.err != nil {
		return authstore.User{}, u.err
	}
	user, ok := u.users[id]
	if !ok {
		return authstore.User{}, authstore.ErrNotFound
	}
	return user, nil
}

// delegationGrant is the grant the exchange hands the policy for user,
// asking for audiences under the consented scopes.
func delegationGrant(user authstore.User, scopes []string, audiences ...string) exchange.Grant {
	return exchange.Grant{
		Subject: exchange.Subject{
			Subject: user.UserID.String(), ClientID: subjectClientID, Scopes: scopes,
			Claims: map[string]any{subjectClaimTenant: user.TenantID.String()},
		},
		Client:    exchange.Client{ID: relayClientID},
		Audiences: audiences,
	}
}

// relayClientID is the MCP server, the client of the exchange.
const relayClientID = "paladin-mcp-server"

// The capability issued for a user acts for that user, in their tenant, on
// the plane asked for, with what they consented to and no further.
func TestDelegationPolicyIssuesForTheUser(t *testing.T) {
	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Roles: []string{apiutil.AdminAudienceRoles[0]}}
	policy := Policy(usersByID{users: map[uuid.UUID]authstore.User{user.UserID: user}}, limes.AbstractUnitCode)

	req, err := policy(t.Context(), delegationGrant(user, []string{auth.OAuthScopeRead}, allPlanes...))
	if err != nil {
		t.Fatal(err)
	}
	if s := req.Subject; s.Type != limes.PrincipalTypeAgent || s.Subject != relayClientID ||
		s.TenantID != user.TenantID || s.Agent == nil || s.Agent.MCPClient != subjectClientID {
		t.Errorf("subject = %+v, want the relay %q for the agent %q in the user's tenant", s, relayClientID, subjectClientID)
	}
	if o := req.OnBehalfOf; o == nil || o.Type != limes.PrincipalTypeUser || o.Subject != user.UserID.String() || o.TenantID != user.TenantID {
		t.Errorf("on behalf of %+v, want the user", o)
	}
	if ib := req.IssuedBy; ib.Type != auth.DelegationIssuer().Type || ib.Subject != auth.DelegationIssuerSubject || req.ClientID != subjectClientID {
		t.Errorf("issued by %+v for %q, want the MCP server for the agent", ib, req.ClientID)
	}
	// No lifetime of its own: the exchange gives it its token's.
	if !slices.Equal(req.Audience, allPlanes) || req.TTL != 0 {
		t.Errorf("audience %v, TTL %v; want %v and none", req.Audience, req.TTL, allPlanes)
	}
	if want, _ := auth.DelegatedOps([]string{auth.OAuthScopeRead}); !slices.Equal(req.Caveats.Ops, want) {
		t.Errorf("ops %v, want %v", req.Caveats.Ops, want)
	}
	// The bridge is an agent: a flagged object stays closed to it, as to
	// any agent's capability without AllowTaintedRead.
	if r, b := req.Caveats.Requests, req.Caveats.Budget; !r.Unlimited || !b.Unlimited || b.UnitCode != limes.AbstractUnitCode {
		t.Errorf("requests %+v, budget %+v; want both unlimited, the budget in %s", r, b, limes.AbstractUnitCode)
	}
	if req.Caveats.AllowTaintedRead {
		t.Error("a delegated capability reads tainted objects")
	}
}

// allPlanes is every plane the MCP server asks a capability for.
var allPlanes = Planes

// A plane the user's roles do not reach is left out of the capability, not
// a reason to refuse the others.
func TestDelegationPolicyLeavesOutWhatTheUserMayNotHold(t *testing.T) {
	member := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	policy := Policy(usersByID{users: map[uuid.UUID]authstore.User{member.UserID: member}}, limes.AbstractUnitCode)
	req, err := policy(t.Context(), delegationGrant(member, []string{auth.OAuthScopeRead}, allPlanes...))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{auth.TokenPlaneData, auth.TokenPlaneIAM}; !slices.Equal(req.Audience, want) {
		t.Errorf("audience %v, want %v", req.Audience, want)
	}
}

// What the user may not hold, or a grant that is no longer the user's, is
// refused with the error the exchange answers for it.
func TestDelegationPolicyRefuses(t *testing.T) {
	member := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	disabled := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Disabled: true}
	users := usersByID{users: map[uuid.UUID]authstore.User{member.UserID: member, disabled.UserID: disabled}}
	policy := Policy(users, limes.AbstractUnitCode)
	read := []string{auth.OAuthScopeRead}

	moved := delegationGrant(member, read, auth.TokenPlaneData)
	moved.Subject.Claims[subjectClaimTenant] = uuid.New().String()
	notAUser := delegationGrant(member, read, auth.TokenPlaneData)
	notAUser.Subject.Subject = "alice"

	for _, tc := range []struct {
		name  string
		grant exchange.Grant
		want  error
	}{
		{"only the admin plane, without an admin role", delegationGrant(member, read, auth.TokenPlaneAdmin), exchange.ErrInvalidTarget},
		{"a plane that is not one", delegationGrant(member, read, auth.TokenPlaneData, "billing"), exchange.ErrInvalidTarget},
		{"a plane by its token audience", delegationGrant(member, read, auth.AudienceData), exchange.ErrInvalidTarget},
		{"nothing asked for", delegationGrant(member, read), exchange.ErrInvalidTarget},
		{"no consent", delegationGrant(member, []string{"openid"}, auth.TokenPlaneData), exchange.ErrInvalidScope},
		{"a disabled user", delegationGrant(disabled, read, auth.TokenPlaneData), exchange.ErrInvalidGrant},
		{"a user who is gone", delegationGrant(authstore.User{UserID: uuid.New(), TenantID: member.TenantID}, read, auth.TokenPlaneData), exchange.ErrInvalidGrant},
		{"a token from another tenant", moved, exchange.ErrInvalidGrant},
		{"a subject that is no user", notAUser, exchange.ErrInvalidGrant},
	} {
		if req, err := policy(t.Context(), tc.grant); !errors.Is(err, tc.want) {
			t.Errorf("%s: %+v, %v; want %v", tc.name, req, err, tc.want)
		}
	}
}

// A directory that fails is the server's failure, not a refusal of the
// grant: the exchange answers it as such and the agent retries.
func TestDelegationPolicyFailsWithTheDirectory(t *testing.T) {
	down := errors.New("pool closed")
	member := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	_, err := Policy(usersByID{err: down}, limes.AbstractUnitCode)(t.Context(),
		delegationGrant(member, []string{auth.OAuthScopeRead}, auth.TokenPlaneData))
	if !errors.Is(err, down) || errors.Is(err, exchange.ErrInvalidGrant) {
		t.Errorf("err = %v, want the directory's failure and no refusal", err)
	}
}
