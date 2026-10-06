package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// roleCapabilityIssuer mirrors apiutil.RoleCapabilityIssuer, spelled out for
// the same reason as roleTenantProvisioner: apiutil imports this package.
const roleCapabilityIssuer = "platform.capability-issuer"

// rolePlatformAdmin mirrors apiutil.RolePlatformAdmin.
const rolePlatformAdmin = "platform.admin"

// authzWithRoles asks the engine, over an EMPTY tenant policy, whether a
// principal holding roles may perform action on its own tenant — the resource
// every admin-plane credential handler sends. The grant has to come from the
// built-in policy: the role belongs to the platform, not to any tenant.
//
// The principal carries no credential kind, so the machine-delete built-in
// (ADR-0012), which admits any machine credential to its own tenant's objects,
// stays out of the answer: what is measured is the role's grant alone.
func authzWithRoles(t *testing.T, action Action, roles ...string) Decision {
	t.Helper()
	tenant := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "apikey:consumer", TenantID: tenant, Roles: roles},
		action,
		&Resource{TenantID: tenant},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s): %v", action, err)
	}
	return dec
}

// The role was named in the handlers and in the roles list, yet no policy
// granted it a single action, so every Issue it made was denied and only
// platform.admin could issue. This is that regression.
func TestCapabilityIssuer_MayIssueRevokeAndRead(t *testing.T) {
	for _, action := range []Action{
		ActionIssueCapability,
		ActionRevokeCapability,
		ActionReadCapability,
	} {
		t.Run(action.String(), func(t *testing.T) {
			if got := authzWithRoles(t, action, roleCapabilityIssuer); got != DecisionAllow {
				t.Fatalf("%s = %v, want Allow", action, got)
			}
		})
	}
}

// The omissions are the reason this role exists instead of platform.admin. A
// leaked issuer credential must not mint or read a long-lived credential,
// reshape a tenant, or touch a single object.
func TestCapabilityIssuer_CarriesNothingElse(t *testing.T) {
	for _, action := range []Action{
		ActionDelegateCapability,
		ActionCreateAPIToken,
		ActionRevokeAPIToken,
		ActionReadAPIToken,
		ActionInspectMCP,
		ActionManageTenant,
		ActionManageBucket,
		ActionManageCollection,
		ActionManageBackend,
		ActionManageUser,
		ActionGrantScopes,
		ActionReadAuditLog,
		ActionGetObject,
		ActionPutObject,
		ActionDeleteObject,
	} {
		t.Run(action.String(), func(t *testing.T) {
			if got := authzWithRoles(t, action, roleCapabilityIssuer); got != DecisionDeny {
				t.Fatalf("%s = %v, want Deny — the capability issuer must not carry it", action, got)
			}
		})
	}
}

// A principal without the role gets nothing from the permit — guards a `when`
// clause written so it holds for everyone, which the positive test above would
// not notice. The tenant provisioner is in the list: the two narrow roles must
// not overlap.
func TestCapabilityIssuer_RoleIsRequired(t *testing.T) {
	for _, roles := range [][]string{nil, {}, {"tenant.admin"}, {roleTenantProvisioner}} {
		if got := authzWithRoles(t, ActionIssueCapability, roles...); got != DecisionDeny {
			t.Fatalf("roles=%v decision = %v, want Deny", roles, got)
		}
	}
}

// A tenant that wants the issuer kept out stays able to: first-forbid wins over
// the built-in permit.
func TestCapabilityIssuer_TenantPolicyCanForbid(t *testing.T) {
	const policy = `forbid(principal, action == Action::"IssueCapability", resource)
when { principal has roles && principal.roles.contains("platform.capability-issuer") };`

	tenant := uuid.New()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "apikey:consumer", TenantID: tenant, Roles: []string{roleCapabilityIssuer}},
		ActionIssueCapability,
		&Resource{TenantID: tenant},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in permit", dec)
	}
}

// platform.admin keeps every credential action, over an empty tenant policy.
func TestPlatformAdmin_HoldsEveryCredentialAction(t *testing.T) {
	for _, action := range []Action{
		ActionIssueCapability,
		ActionDelegateCapability,
		ActionRevokeCapability,
		ActionReadCapability,
		ActionCreateAPIToken,
		ActionRevokeAPIToken,
		ActionReadAPIToken,
		ActionInspectMCP,
	} {
		t.Run(action.String(), func(t *testing.T) {
			if got := authzWithRoles(t, action, rolePlatformAdmin); got != DecisionAllow {
				t.Fatalf("%s = %v, want Allow", action, got)
			}
		})
	}
}
