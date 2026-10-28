package apiutil

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// DeniedByPolicy is what a caller the policy does not allow is told.
const DeniedByPolicy = "denied by policy"

// Authorize asks policy whether the caller on ctx may take action on
// resource, and answers the caller: Unauthenticated without one,
// PermissionDenied when the policy does not allow it, and the engine's own
// failure mapped as MapError maps any other. The engine fills in the time.
func Authorize(ctx context.Context, policy cedar.Authorizer, action cedar.Action, resource *cedar.Resource) (*auth.Principal, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := Decide(ctx, policy, CedarPrincipal(p), action, resource, cedar.RequestContext{}); err != nil {
		return nil, err
	}
	return p, nil
}

// AuthorizeOwnTenant is Authorize on the caller's own tenant: for an action
// that is the caller's to take wherever it reaches, whose reach past its
// tenant the handler decides.
func AuthorizeOwnTenant(ctx context.Context, policy cedar.Authorizer, action cedar.Action) (*auth.Principal, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := Decide(ctx, policy, CedarPrincipal(p), action, &cedar.Resource{TenantID: p.TenantID, TenantSlug: p.TenantSlug}, cedar.RequestContext{}); err != nil {
		return nil, err
	}
	return p, nil
}

// Decide asks policy whether principal may take action on resource in the
// request rc describes, for a gate that builds the principal itself. The
// engine fills in rc's time when it has none.
func Decide(ctx context.Context, policy cedar.Authorizer, principal *cedar.Principal, action cedar.Action, resource *cedar.Resource, rc cedar.RequestContext) error {
	decision, err := policy.IsAuthorized(ctx, principal, action, resource, rc)
	if err != nil {
		return MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, DeniedByPolicy)
	}
	return nil
}
