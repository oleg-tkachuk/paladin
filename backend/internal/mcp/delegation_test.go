package mcp

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// delegationTTL is the longest a delegated capability lives in these tests.
const delegationTTL = 15 * time.Minute

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
		Audiences: audiences,
	}
}

// The capability issued for a user acts for that user, in their tenant, on
// the plane asked for, with what they consented to and no further.
func TestDelegationPolicyIssuesForTheUser(t *testing.T) {
	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Roles: []string{apiutil.AdminAudienceRoles[0]}}
	policy := DelegationPolicy(usersByID{users: map[uuid.UUID]authstore.User{user.UserID: user}}, delegationTTL)

	for _, plane := range []string{auth.TokenPlaneData, auth.TokenPlaneAdmin, auth.TokenPlaneIAM} {
		req, err := policy(t.Context(), delegationGrant(user, []string{auth.OAuthScopeRead}, plane))
		if err != nil {
			t.Fatalf("%s: %v", plane, err)
		}
		if s := req.Subject; s.Type != limes.PrincipalTypeAgent || s.Subject != subjectClientID ||
			s.TenantID != user.TenantID || s.Agent == nil || s.Agent.MCPClient != subjectClientID {
			t.Errorf("%s: subject = %+v, want the agent %q in the user's tenant", plane, s, subjectClientID)
		}
		if o := req.OnBehalfOf; o == nil || o.Type != limes.PrincipalTypeUser || o.Subject != user.UserID.String() || o.TenantID != user.TenantID {
			t.Errorf("%s: on behalf of %+v, want the user", plane, o)
		}
		if ib := req.IssuedBy; ib.Type != auth.DelegationIssuer().Type || ib.Subject != auth.DelegationIssuerSubject || req.ClientID != subjectClientID {
			t.Errorf("%s: issued by %+v for %q, want the MCP server for the agent", plane, req.IssuedBy, req.ClientID)
		}
		if !slices.Equal(req.Audience, []string{plane}) || req.TTL != delegationTTL {
			t.Errorf("%s: audience %v, TTL %v; want [%s] and %v", plane, req.Audience, req.TTL, plane, delegationTTL)
		}
		if want, _ := auth.DelegatedOps([]string{auth.OAuthScopeRead}); !slices.Equal(req.Caveats.Ops, want) {
			t.Errorf("%s: ops %v, want %v", plane, req.Caveats.Ops, want)
		}
	}
}

// What the user may not hold, or a grant that is no longer the user's, is
// refused with the error the exchange answers for it.
func TestDelegationPolicyRefuses(t *testing.T) {
	member := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	disabled := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Disabled: true}
	users := usersByID{users: map[uuid.UUID]authstore.User{member.UserID: member, disabled.UserID: disabled}}
	policy := DelegationPolicy(users, delegationTTL)
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
		{"the admin plane without an admin role", delegationGrant(member, read, auth.TokenPlaneAdmin), exchange.ErrInvalidTarget},
		{"a plane that is not one", delegationGrant(member, read, "billing"), exchange.ErrInvalidTarget},
		{"two planes at once", delegationGrant(member, read, auth.TokenPlaneData, auth.TokenPlaneIAM), exchange.ErrInvalidTarget},
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
	_, err := DelegationPolicy(usersByID{err: down}, delegationTTL)(t.Context(),
		delegationGrant(member, []string{auth.OAuthScopeRead}, auth.TokenPlaneData))
	if !errors.Is(err, down) || errors.Is(err, exchange.ErrInvalidGrant) {
		t.Errorf("err = %v, want the directory's failure and no refusal", err)
	}
}
