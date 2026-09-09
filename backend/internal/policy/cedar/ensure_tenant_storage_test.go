package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// EnsureTenantStorage is a built-in permit with exactly one gate on it:
// principal.tenant_id == resource.tenant_id. Nothing referenced the action
// anywhere in the suite — not the grant, not the refusal — so inverting that
// comparison survived, and inverted it reads "a member may provision storage
// in every tenant EXCEPT their own".
//
// Two of the three built-ins carrying this same gate are held: ReadTenant by
// TestBuiltin_ReadOwnTenant and the machine delete pair by
// TestMachineDelete_DeniedCrossTenant. This is the third.
//
// The tenant policy is empty on purpose, matching the other built-in tests:
// the grant has to come from the built-in, because a tenant provisioned
// before this policy existed has its stored policy frozen at creation and
// would otherwise never receive it.
func authzEnsureStorage(t *testing.T, sameTenant bool, roles []string) Decision {
	t.Helper()
	tid := uuid.New()
	resourceTenant := tid
	if !sameTenant {
		resourceTenant = uuid.New()
	}

	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "member@acme", TenantID: tid, Roles: roles},
		ActionEnsureTenantStorage,
		&Resource{TenantID: resourceTenant},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(EnsureTenantStorage): %v", err)
	}
	return dec
}

// Self-service is the whole point: a plain member, holding no role at all,
// may bring their own tenant's storage into existence.
func TestEnsureTenantStorage_AllowedInItsOwnTenant(t *testing.T) {
	if got := authzEnsureStorage(t, true, nil); got != DecisionAllow {
		t.Fatalf("own tenant = %v, want Allow — self-service provisioning is the point of this built-in", got)
	}
}

// And the gate is the only thing standing between that and provisioning
// somebody else's tenant. A member with no role must not reach across.
func TestEnsureTenantStorage_DeniedCrossTenant(t *testing.T) {
	if got := authzEnsureStorage(t, false, nil); got == DecisionAllow {
		t.Fatal("another tenant = Allow; a member provisioned storage outside their own tenant")
	}
}

// The grant is unconditional on roles by design — it must work for a tenant
// whose members hold nothing — so a role must neither be required for the
// own-tenant case nor buy the cross-tenant one. The second half is the part
// that matters: an ordinary tenant role must not become a cross-tenant
// provisioning credential.
func TestEnsureTenantStorage_RolesDoNotWidenIt(t *testing.T) {
	for _, roles := range [][]string{
		nil,
		{"tenant.user"},
		{"collection:admin"},
	} {
		if got := authzEnsureStorage(t, true, roles); got != DecisionAllow {
			t.Errorf("own tenant with roles %v = %v, want Allow", roles, got)
		}
		if got := authzEnsureStorage(t, false, roles); got == DecisionAllow {
			t.Errorf("another tenant with roles %v = Allow, want a denial", roles)
		}
	}
}
