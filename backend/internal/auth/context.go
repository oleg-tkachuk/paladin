// Package auth carries authenticated identity through the request context.
//
// Tenant identity is propagated as an opaque value via context — never via
// request fields. Handlers read the tenant via TenantFromContext.
package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Principal is the authenticated caller.
type Principal struct {
	// TenantID is the tenant the caller is scoped to. Empty only for
	// super-admin principals invoking cross-tenant RPCs.
	TenantID uuid.UUID
	// TenantSlug is the tenant's human-readable slug. Optional during the
	// rollout — when set, becomes the canonical Cedar Tenant UID. Carried
	// from the JWT `tenant_slug` claim minted by the issuer.
	TenantSlug string
	// Subject is the stable identifier inside the tenant (user ID, service
	// account, etc.). Carried verbatim from the JWT `sub` claim.
	Subject string
	// Roles are RBAC strings used by Cedar policies (e.g. "tenant.admin",
	// "platform.admin", "mcp.operator").
	Roles []string
	// Scopes narrow the principal to a subset of resources. Cedar evaluates
	// resource membership against these in addition to roles. Empty means
	// "no per-resource restriction" (Cedar default policies still apply).
	Scopes []Scope
	// Audience is the JWT `aud` claim — used by handlers to assert that a
	// caller hitting the admin plane wasn't issued a data-plane token.
	Audience string
	// PrincipalKind distinguishes a User-bound JWT from an ApiKey-derived
	// token. Cedar policies may key on this for blast-radius limits.
	Kind PrincipalKind
	// Labels are free-form claim attributes exposed to Cedar as principal
	// attributes.
	Labels map[string]string
}

// PrincipalKind enumerates the originating credential type.
type PrincipalKind uint8

const (
	PrincipalKindUnspecified PrincipalKind = iota
	PrincipalKindUser
	PrincipalKindApiKey
	PrincipalKindServiceAccount // platform-issued, e.g. MCP server
	// PrincipalKindCapability is a principal established by a verified
	// capability token: short-lived, tenant-scoped, individually revocable,
	// and carrying no roles. It authenticates its bearer AS the tenant the
	// capability was issued for, which is what lets one service credential
	// mint per-tenant access without holding a long-lived credential per
	// tenant. Authorisation beyond identity stays with the capability's own
	// caveats (ops, resource prefixes, budget) — enforced by the interceptor
	// before this principal is ever established — and with Cedar, whose
	// data-plane policies gate on tenant membership rather than on roles.
	PrincipalKindCapability
)

// HasRole reports whether the principal carries the given role string.
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type principalKey struct{}

// WithPrincipal returns a child context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the authenticated principal or an error when
// unset. Handlers should return connect.CodeUnauthenticated on error.
func PrincipalFromContext(ctx context.Context) (*Principal, error) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	if !ok || p == nil {
		return nil, ErrNoPrincipal
	}
	return p, nil
}

// TenantFromContext is a shortcut for handlers that only need the tenant ID.
// Returns ErrNoPrincipal when unauthenticated and ErrNoTenant when the
// principal is a cross-tenant admin without a tenant binding.
func TenantFromContext(ctx context.Context) (uuid.UUID, error) {
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if p.TenantID == uuid.Nil {
		return uuid.Nil, ErrNoTenant
	}
	return p.TenantID, nil
}

var (
	ErrNoPrincipal = errors.New("auth: no authenticated principal")
	ErrNoTenant    = errors.New("auth: principal is not bound to a tenant")
)
