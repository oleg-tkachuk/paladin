package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// aliceLogin is the login name of the user whose settings are asked about. It
// is the resource's `subject`; the principal's subject is the user's id, as an
// IAM token carries it. A fixture that put the login name in both places
// passed while the grant matched nobody in a cluster.
const aliceLogin = "alice"

// authzSettings asks the engine, over an EMPTY tenant policy, whether the user
// callerID of tenant may perform action on the settings of user targetID in
// targetTenant. Empty on purpose: a tenant created before this grant has its
// stored policy frozen, so it must come from the built-in.
func authzSettings(t *testing.T, action Action, callerID, tenant, targetID, targetTenant uuid.UUID) Decision {
	t.Helper()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: callerID.String(), TenantID: tenant, Kind: "user", Roles: []string{"tenant.user"}},
		action,
		&Resource{TenantID: targetTenant, TargetUserID: targetID, TargetSubject: aliceLogin},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s): %v", action, err)
	}
	return dec
}

func TestBuiltin_OwnUserSettings(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	alice, bob := uuid.New(), uuid.New()
	for _, action := range []Action{ActionReadUserSettings, ActionManageUserSettings} {
		t.Run(action.String(), func(t *testing.T) {
			if got := authzSettings(t, action, alice, tenant, alice, tenant); got != DecisionAllow {
				t.Errorf("own settings = %v, want Allow", got)
			}
			if got := authzSettings(t, action, bob, tenant, alice, tenant); got != DecisionDeny {
				t.Errorf("a teammate's settings = %v, want Deny", got)
			}
			if got := authzSettings(t, action, alice, tenant, alice, other); got != DecisionDeny {
				t.Errorf("the same id under another tenant = %v, want Deny", got)
			}
		})
	}
}

// A principal whose subject is the target's LOGIN NAME is not that user: the
// grant keys on the id. Pins the identity model, so the grant cannot drift
// back to comparing login names.
func TestBuiltin_OwnUserSettingsKeysOnTheUserID(t *testing.T) {
	tenant := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: aliceLogin, TenantID: tenant, Kind: "user"},
		ActionReadUserSettings,
		&Resource{TenantID: tenant, TargetUserID: uuid.New(), TargetSubject: aliceLogin},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a login name is not a user id", dec)
	}
}

// Listing a tenant's settings is checked against the Tenant, which has no
// user_id: the self-service grant must not turn into a read of everyone's.
func TestBuiltin_OwnUserSettingsDoesNotCoverTheTenant(t *testing.T) {
	tenant := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: uuid.NewString(), TenantID: tenant, Kind: "user", Roles: []string{"tenant.user"}},
		ActionReadUserSettings,
		&Resource{TenantID: tenant},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("listing the tenant's settings = %v, want Deny", dec)
	}
}

// A tenant can still refuse the grant: first-forbid wins.
func TestBuiltin_OwnUserSettingsTenantCanForbid(t *testing.T) {
	const policy = `forbid(principal, action == Action::"ManageUserSettings", resource);`
	tenant, alice := uuid.New(), uuid.New()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: alice.String(), TenantID: tenant, Kind: "user"},
		ActionManageUserSettings,
		&Resource{TenantID: tenant, TargetUserID: alice, TargetSubject: aliceLogin},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in", dec)
	}
}
