package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// roleTenantProvisioner mirrors apiutil.RoleTenantProvisioner. Spelled out
// rather than imported: apiutil imports this package, so referencing the
// constant here would close an import cycle. A literal is also the stricter
// test — the policy text below is matched against the wire value, not against
// whatever the constant happens to say.
const roleTenantProvisioner = "platform.tenant-provisioner"

// authzAsProvisioner asks the engine whether a principal holding ONLY
// platform.tenant-provisioner may perform action on another tenant's resource.
// The tenant policy is empty on purpose: the grant has to come from the
// built-in policy, or a freshly created tenant — whose stored policy is written
// only after provisioning — could never be provisioned at all.
func authzAsProvisioner(t *testing.T, action string) Decision {
	t.Helper()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{
			Subject:  "consumer-provisioner",
			TenantID: uuid.New(), // a DIFFERENT tenant from the resource
			Roles:    []string{roleTenantProvisioner},
		},
		action,
		&Resource{TenantID: uuid.New()},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized(%s): %v", action, err)
	}

	return dec
}

// Everything provisioning a tenant's storage takes: the tenant row, the bucket,
// the object keys, and the policy write (which routes through ManageTenant).
// Cross-tenant by design — the caller is provisioning somebody else's tenant.
func TestTenantProvisioner_MayProvision(t *testing.T) {
	for _, action := range []string{
		ActionManageTenant,
		ActionReadTenant,
		ActionManageBucket,
		ActionReadBucket,
		ActionManageCollection,
		ActionBindCollectionToBucket,
	} {
		t.Run(action, func(t *testing.T) {
			if got := authzAsProvisioner(t, action); got != DecisionAllow {
				t.Fatalf("%s = %v, want Allow", action, got)
			}
		})
	}
}

// The omissions are the reason this role exists instead of platform.admin.
// A leaked provisioner credential must not be able to read or destroy a single
// object, mint a credential, or touch a storage backend.
func TestTenantProvisioner_MayNotReachData(t *testing.T) {
	for _, action := range []string{
		ActionGetObject,
		ActionPutObject,
		ActionDeleteObject,
		ActionHeadObject,
		ActionCopyObject,
		ActionPresignGet,
		ActionPresignPut,
		ActionManageBackend,
		ActionRotateBackendCredentials,
		ActionManageUser,
		ActionResetPassword,
		ActionGrantScopes,
		ActionReadAuditLog,
	} {
		t.Run(action, func(t *testing.T) {
			if got := authzAsProvisioner(t, action); got != DecisionDeny {
				t.Fatalf("%s = %v, want Deny — the provisioner role must not carry it", action, got)
			}
		})
	}
}

// A tenant that wants to keep provisioning out stays able to: first-forbid wins
// over the built-in permit, same as every other built-in.
func TestTenantProvisioner_TenantPolicyCanForbid(t *testing.T) {
	const policy = `forbid(principal, action == Action::"ManageTenant", resource)
when { principal has roles && principal.roles.contains("platform.tenant-provisioner") };`

	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{
			Subject:  "consumer-provisioner",
			TenantID: uuid.New(),
			Roles:    []string{roleTenantProvisioner},
		},
		ActionManageTenant,
		&Resource{TenantID: uuid.New()},
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny — a tenant forbid must beat the built-in permit", dec)
	}
}

// A principal WITHOUT the role gets nothing from this permit. Guards against
// writing the `when` clause in a way that is true for everyone — a mistake that
// would hand tenant creation to every authenticated caller and still pass the
// positive test above.
func TestTenantProvisioner_RoleIsRequired(t *testing.T) {
	e := NewEngine(fakeStore{}, time.Minute)
	for _, roles := range [][]string{nil, {}, {"tenant.user"}, {"platform.capability-issuer"}} {
		dec, err := e.IsAuthorized(context.Background(),
			&Principal{Subject: "somebody", TenantID: uuid.New(), Roles: roles},
			ActionManageTenant,
			&Resource{TenantID: uuid.New()},
			RequestContext{},
		)
		if err != nil {
			t.Fatalf("IsAuthorized(roles=%v): %v", roles, err)
		}
		if dec != DecisionDeny {
			t.Fatalf("roles=%v decision = %v, want Deny", roles, dec)
		}
	}
}
