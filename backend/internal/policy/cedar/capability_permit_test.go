package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
)

// Capability operations as limes spells them on the wire (limes.OpGet …).
// Spelled out for the reason the credential kinds are: the policy text must
// match the wire value, not whatever a constant happens to say.
const (
	capOpGet    = "get"
	capOpList   = "list"
	capOpPut    = "put"
	capOpDelete = "delete"
	capOpManage = "manage"
	// capOpNone is a request whose handler asserted nothing.
	capOpNone = ""
)

// authzCapability asks whether a principal of the given kind, whose request
// stamped op, may perform action on an object. The tenant policy is EMPTY
// unless policy says otherwise: the grant has to come from the built-in.
func authzCapability(t *testing.T, kind, op string, action Action, sameTenant bool, policy string) Decision {
	t.Helper()
	tid := uuid.New()
	resourceTenant := tid
	if !sameTenant {
		resourceTenant = uuid.New()
	}

	ctx := capop.WithHolder(context.Background())
	if op != capOpNone {
		capop.Stamp(ctx, op)
	}

	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(ctx,
		&Principal{Subject: "agent:probe", TenantID: tid, Kind: kind},
		action,
		&Resource{TenantID: resourceTenant, Collection: "acme-avatars", Key: "a/b.png"},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s, %q, %s): %v", kind, op, action, err)
	}

	return dec
}

// What each admitted operation unlocks for a capability in its own tenant.
func TestCapabilityPermit_GrantsTheActionsOfItsOperation(t *testing.T) {
	cases := []struct {
		op     string
		action Action
	}{
		{capOpGet, ActionGetObject},
		{capOpGet, ActionPresignGet},
		{capOpList, ActionGetObject},
		{capOpList, ActionPresignGet},
		{capOpPut, ActionPutObject},
		{capOpPut, ActionPresignPut},
		{capOpPut, ActionUpdateObject},
		{capOpPut, ActionCopyObject},
	}
	for _, c := range cases {
		t.Run(c.op+"/"+c.action.String(), func(t *testing.T) {
			if got := authzCapability(t, kindCapability, c.op, c.action, true, ""); got != DecisionAllow {
				t.Fatalf("%s under %q = %v, want Allow", c.action, c.op, got)
			}
		})
	}
}

// An action outside the operation the caveats admitted stays denied: the
// permit binds the two, so a checked read cannot authorize a write, and an
// unchecked request authorizes nothing.
func TestCapabilityPermit_DeniesActionsOutsideItsOperation(t *testing.T) {
	cases := []struct {
		name   string
		op     string
		action Action
	}{
		{"nothing asserted, read", capOpNone, ActionGetObject},
		{"nothing asserted, upload", capOpNone, ActionPresignPut},
		{"read admitted, upload asked", capOpGet, ActionPresignPut},
		{"list admitted, write asked", capOpList, ActionPutObject},
		{"write admitted, read asked", capOpPut, ActionGetObject},
		{"write admitted, download asked", capOpPut, ActionPresignGet},
		{"manage admitted, read asked", capOpManage, ActionGetObject},
		{"manage admitted, taint asked", capOpManage, ActionSetObjectTaint},
		{"delete admitted, write asked", capOpDelete, ActionPutObject},
		{"read admitted, lock asked", capOpGet, ActionReadObjectLock},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := authzCapability(t, kindCapability, c.op, c.action, true, ""); got != DecisionDeny {
				t.Fatalf("%s under %q = %v, want Deny", c.action, c.op, got)
			}
		})
	}
}

// The grant is a capability's alone, and only in its own tenant.
func TestCapabilityPermit_OnlyACapabilityInItsOwnTenant(t *testing.T) {
	for _, kind := range []string{kindUser, kindAPIKey, kindServiceAccount} {
		t.Run(kind, func(t *testing.T) {
			if got := authzCapability(t, kind, capOpGet, ActionGetObject, true, ""); got != DecisionDeny {
				t.Fatalf("%s reading = %v, want Deny — no role, no permit", kind, got)
			}
		})
	}
	t.Run("another tenant", func(t *testing.T) {
		if got := authzCapability(t, kindCapability, capOpGet, ActionGetObject, false, ""); got != DecisionDeny {
			t.Fatalf("cross-tenant read = %v, want Deny", got)
		}
	})
}

// A tenant that wants capabilities held to its own rules can say so:
// first-forbid beats the built-in.
func TestCapabilityPermit_TenantPolicyCanForbid(t *testing.T) {
	const policy = `forbid(principal, action == Action::"PresignPut", resource)
when { principal has kind && principal.kind == "capability" };`

	if got := authzCapability(t, kindCapability, capOpPut, ActionPresignPut, true, policy); got != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in", got)
	}
	if got := authzCapability(t, kindCapability, capOpGet, ActionGetObject, true, policy); got != DecisionAllow {
		t.Fatalf("an unforbidden read = %v, want Allow", got)
	}
}

// RequestContext has no field for the operation: only the request's own
// holder sets it, so a handler cannot claim an operation nobody checked.
func TestCapabilityPermit_ReadsTheOperationFromTheRequestOnly(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "agent:probe", TenantID: tid, Kind: kindCapability},
		ActionGetObject,
		&Resource{TenantID: tid, Collection: "acme-avatars", Key: "a/b.png"},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision without a holder = %v, want Deny", dec)
	}
}
