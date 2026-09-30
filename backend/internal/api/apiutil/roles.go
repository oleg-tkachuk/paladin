package apiutil

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// Standard role names. v2 uses dot-separated tier.area form.
const (
	RolePlatformAdmin = "platform.admin"
	RoleIAMAdmin      = "iam.admin"
	RoleBucketAdmin   = "bucket.admin"
	RoleTenantAdmin   = "tenant.admin"
	RoleTenantUser    = "tenant.user"
	RoleMCPOperator   = "mcp.operator"
	// RoleCapabilityIssuer may mint a capability for ANY tenant and nothing
	// else: it cannot create or delete a tenant, and it cannot mint an API
	// token. It exists so a consumer serving many tenants can hand out
	// short-lived, tenant-scoped capabilities without holding either a
	// long-lived credential per tenant or platform.admin, whose leak is
	// unbounded.
	RoleCapabilityIssuer = "platform.capability-issuer"
	// RoleTenantProvisioner may bring a tenant's storage into existence —
	// create the tenant, its bucket and its object keys, and set its inherited
	// policy — for ANY tenant, and nothing else. It cannot delete, purge,
	// rename or restore a tenant, it cannot rewrite a tenant's other fields,
	// it cannot mint credentials, and it has no data-plane reach: it cannot
	// read or write a single object.
	//
	// It exists because provisioning has to happen automatically when a
	// consumer creates an account, and the only credential that could do it
	// was platform.admin — whose leak deletes every tenant's data. Same
	// reasoning as RoleCapabilityIssuer above: the authority a machine needs
	// standing is the authority worth naming.
	RoleTenantProvisioner = "platform.tenant-provisioner"
)

// AdminAudienceRoles are the roles IAM issues the paladin-admin audience to:
// each one's work is on the admin plane. tenant.user and mcp.operator have
// none there. An explicit list rather than a name pattern, so a new role
// reaches the admin plane only by being added here. The console mirrors it
// (frontend/src/constants/roles.ts), and scripts/admin-audience-roles.test.sh
// keeps the two equal.
var AdminAudienceRoles = []string{
	RolePlatformAdmin,
	RoleTenantAdmin,
	RoleBucketAdmin,
	RoleIAMAdmin,
	RoleTenantProvisioner,
	RoleCapabilityIssuer,
}

// RequireRole returns Unauthenticated when no principal is present and
// PermissionDenied when the principal lacks the role.
func RequireRole(ctx context.Context, role string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !p.HasRole(role) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("role %q required", role))
	}
	return nil
}

// RequireAnyRole accepts the request if the principal holds at least one of
// the listed roles.
func RequireAnyRole(ctx context.Context, roles ...string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	for _, r := range roles {
		if p.HasRole(r) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("insufficient role"))
}

// HasRole is a convenience predicate (no error wrapping) for handler-side
// branching where neither outcome should be a connect error.
func HasRole(ctx context.Context, role string) bool {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return false
	}
	return p.HasRole(role)
}
