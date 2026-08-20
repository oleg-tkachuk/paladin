package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Wire names for the credential kinds, mirroring auth.PrincipalKind.String().
// Spelled out rather than imported (auth imports this package), and a literal is
// the stricter test anyway: the policy text matches the wire value, not whatever
// the constant happens to say.
const (
	kindUser           = "user"
	kindAPIKey         = "api_key"
	kindServiceAccount = "service_account"
	kindCapability     = "capability"
)

// authzDelete asks whether a principal of the given kind may delete an object.
// sameTenant chooses whether the object belongs to the principal's own tenant.
// The tenant policy is EMPTY: the grant has to come from the built-in, or a
// tenant provisioned before this existed would never get it.
func authzDelete(t *testing.T, kind, action string, sameTenant bool) Decision {
	t.Helper()
	tid := uuid.New()
	resourceTenant := tid
	if !sameTenant {
		resourceTenant = uuid.New()
	}

	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "consumer", TenantID: tid, Kind: kind},
		action,
		&Resource{TenantID: resourceTenant, Collection: "acme-avatars", Key: "a/b.png"},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s, %s): %v", kind, action, err)
	}

	return dec
}

// A machine credential owns the object lifecycle: it records an object, later
// removes the record, and must be able to remove the object with it. It cannot
// hold a role to do so — an API token's principal carries none and a capability
// carries none by design — so without this the delete simply never worked.
func TestMachineDelete_AllowedInItsOwnTenant(t *testing.T) {
	for _, kind := range []string{kindAPIKey, kindServiceAccount, kindCapability} {
		for _, action := range []string{ActionDeleteObject, ActionRestoreObject} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				if got := authzDelete(t, kind, action, true); got != DecisionAllow {
					t.Fatalf("%s %s = %v, want Allow", kind, action, got)
				}
			})
		}
	}
}

// A person still needs the role. Deletion is destructive, and "a tenant member
// may not casually delete" is the default this permit must not erode — it widens
// what MACHINES may do, nothing else.
func TestMachineDelete_StillDeniedForAUser(t *testing.T) {
	for _, action := range []string{ActionDeleteObject, ActionRestoreObject} {
		t.Run(action, func(t *testing.T) {
			if got := authzDelete(t, kindUser, action, true); got != DecisionDeny {
				t.Fatalf("user %s = %v, want Deny", action, got)
			}
		})
	}
}

// A principal whose kind was never populated gets nothing. That is the
// fail-closed default for an authorization site that builds its principal by
// hand instead of through apiutil.CedarPrincipal.
func TestMachineDelete_DeniedWhenKindIsUnknown(t *testing.T) {
	if got := authzDelete(t, "", ActionDeleteObject, true); got != DecisionDeny {
		t.Fatalf("empty kind = %v, want Deny", got)
	}
}

// Own tenant only. A machine credential must not reach another tenant's objects
// even for a delete it would be allowed to perform at home.
func TestMachineDelete_DeniedCrossTenant(t *testing.T) {
	for _, kind := range []string{kindAPIKey, kindServiceAccount, kindCapability} {
		t.Run(kind, func(t *testing.T) {
			if got := authzDelete(t, kind, ActionDeleteObject, false); got != DecisionDeny {
				t.Fatalf("%s cross-tenant delete = %v, want Deny", kind, got)
			}
		})
	}
}

// The permit is deliberately the delete family and nothing more. Update and Copy
// stay role-gated: they are not what a lifecycle owner needs, and widening the
// grant to "whatever a machine might want" is how a narrow permit stops being
// narrow.
func TestMachineDelete_DoesNotWidenToOtherMutations(t *testing.T) {
	for _, action := range []string{ActionUpdateObject, ActionCopyObject} {
		t.Run(action, func(t *testing.T) {
			if got := authzDelete(t, kindCapability, action, true); got != DecisionDeny {
				t.Fatalf("capability %s = %v, want Deny", action, got)
			}
		})
	}
}

// A tenant that wants its machines held to the role can say so: first-forbid
// beats the built-in, same as every other built-in permit.
func TestMachineDelete_TenantPolicyCanForbid(t *testing.T) {
	const policy = `forbid(principal, action == Action::"DeleteObject", resource)
when { principal has kind && principal.kind == "capability" };`

	tid := uuid.New()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "consumer", TenantID: tid, Kind: kindCapability},
		ActionDeleteObject,
		&Resource{TenantID: tid, Collection: "acme-avatars", Key: "a/b.png"},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in", dec)
	}
}
