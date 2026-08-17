package apiutil

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/auth"
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
)

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
