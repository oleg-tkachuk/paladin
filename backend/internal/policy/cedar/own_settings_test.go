package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// authzOwnSettings asks the engine, over an EMPTY tenant policy, whether the
// principal "alice" of tenant may perform action on the settings of
// targetSubject in targetTenant. Empty on purpose: a tenant created before
// this grant has its stored policy frozen, so it must come from the built-in.
func authzOwnSettings(t *testing.T, action Action, tenant, targetTenant uuid.UUID, targetSubject string) Decision {
	t.Helper()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "alice", TenantID: tenant, Kind: "user", Roles: []string{"tenant.user"}},
		action,
		&Resource{TenantID: targetTenant, TargetUserID: uuid.New(), TargetSubject: targetSubject},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s): %v", action, err)
	}
	return dec
}

func TestBuiltin_OwnUserSettings(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	for _, action := range []Action{ActionReadUserSettings, ActionManageUserSettings} {
		t.Run(action.String(), func(t *testing.T) {
			if got := authzOwnSettings(t, action, tenant, tenant, "alice"); got != DecisionAllow {
				t.Errorf("own settings = %v, want Allow", got)
			}
			if got := authzOwnSettings(t, action, tenant, tenant, "bob"); got != DecisionDeny {
				t.Errorf("a teammate's settings = %v, want Deny", got)
			}
			if got := authzOwnSettings(t, action, tenant, other, "alice"); got != DecisionDeny {
				t.Errorf("a same-named user in another tenant = %v, want Deny", got)
			}
		})
	}
}

// Listing a tenant's settings is checked against the Tenant, which has no
// subject: the self-service grant must not turn into a read of everyone's.
func TestBuiltin_OwnUserSettingsDoesNotCoverTheTenant(t *testing.T) {
	tenant := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "alice", TenantID: tenant, Kind: "user", Roles: []string{"tenant.user"}},
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
	tenant := uuid.New()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "alice", TenantID: tenant, Kind: "user"},
		ActionManageUserSettings,
		&Resource{TenantID: tenant, TargetUserID: uuid.New(), TargetSubject: "alice"},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in", dec)
	}
}
