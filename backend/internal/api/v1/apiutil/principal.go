package apiutil

import (
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ScopeStrings flattens an auth.Scope slice into the wire-form strings that
// Cedar's `principal.scopes` Set<String> expects. Returned in declaration
// order (Cedar set semantics ignore order, but stable output simplifies tests).
func ScopeStrings(scopes []auth.Scope) []string {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, s.String())
	}
	return out
}

// CedarPrincipal lifts an auth.Principal into the Cedar evaluation form.
//
// All ~20 handler call sites used to construct `cedar.Principal{...}` literals
// inline, which made it easy to forget a field — TenantSlug and Scopes were
// silently dropped on most paths until this helper landed. New code should
// prefer this function over the literal form.
//
// The returned value is the principal-as-actor; the resource side is built
// separately via cedar.Resource.
func CedarPrincipal(p *auth.Principal) *cedar.Principal {
	return CedarPrincipalFor(p, uuid.Nil)
}

// CedarPrincipalFor is CedarPrincipal with the tenant pinned.
//
// `tenantOverride` is for handlers that target a cross-tenant resource but still
// authorize as the calling principal (a platform admin reading another tenant's
// data): the resource tenant must be the one Cedar compares against, or a
// tenant-equality policy would silently evaluate against the caller's own.
// uuid.Nil keeps the principal's own tenant.
func CedarPrincipalFor(p *auth.Principal, tenantOverride uuid.UUID) *cedar.Principal {
	if p == nil {
		return &cedar.Principal{}
	}
	tenantID := p.TenantID
	if tenantOverride != uuid.Nil {
		tenantID = tenantOverride
	}

	return &cedar.Principal{
		Subject:    p.Subject,
		TenantID:   tenantID,
		TenantSlug: p.TenantSlug,
		// The credential type, so a policy can tell a machine from a person.
		// Populated HERE and only here, which is why every authorization site
		// should build its principal through this helper rather than a literal:
		// a site that forgets the field does not fail loudly, it just stops
		// matching the built-in policies that read it.
		Kind:   p.Kind.String(),
		Roles:  p.Roles,
		Scopes: ScopeStrings(p.Scopes),
	}
}
