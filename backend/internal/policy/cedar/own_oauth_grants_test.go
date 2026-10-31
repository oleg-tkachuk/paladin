package cedar

import (
	"context"
	"testing"
	"time"
	"uuid"
)

// Over an EMPTY tenant policy — the built-in alone decides — who may list and
// end a user's OAuth grants, and list the capabilities acting for the user.
func TestBuiltin_OAuthGrants(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	alice, bob := uuid.New(), uuid.New()
	ask := func(t *testing.T, action Action, caller uuid.UUID, callerTenant uuid.UUID, roles []string, targetTenant uuid.UUID) Decision {
		t.Helper()
		e := NewEngine(fakeStore{}, time.Minute)
		dec, err := e.IsAuthorized(context.Background(),
			&Principal{Subject: caller.String(), TenantID: callerTenant, Kind: "user", Roles: roles},
			action,
			&Resource{TenantID: targetTenant, TargetUserID: alice, TargetSubject: aliceLogin},
			RequestContext{},
		)
		if err != nil {
			t.Fatalf("IsAuthorized(%s): %v", action, err)
		}
		return dec
	}
	user := []string{"tenant.user"}
	admin := []string{"tenant.admin"}
	for _, action := range []Action{ActionReadOAuthGrants, ActionRevokeOAuthGrant, ActionReadUserCapabilities} {
		t.Run(action.String(), func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				caller uuid.UUID
				tenant uuid.UUID
				roles  []string
				target uuid.UUID
				want   Decision
			}{
				{"own grants", alice, tenant, user, tenant, DecisionAllow},
				{"a teammate's", bob, tenant, user, tenant, DecisionDeny},
				{"the tenant admin's teammate", bob, tenant, admin, tenant, DecisionAllow},
				{"another tenant's admin", bob, other, admin, tenant, DecisionDeny},
				{"the same id under another tenant", alice, other, user, tenant, DecisionDeny},
			} {
				if got := ask(t, action, tc.caller, tc.tenant, tc.roles, tc.target); got != tc.want {
					t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
				}
			}
		})
	}
}
