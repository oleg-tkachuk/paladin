package apiutil

import (
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
// `tenantOverride` lets the caller pin a different tenant on the resulting
// principal — used by handlers that target cross-tenant resources but still
// authorize as the calling principal (e.g. a platform admin reading another
// tenant's data). When uuid.Nil, the principal's own tenant is used.
//
// The returned value is the principal-as-actor; the resource side is built
// separately via cedar.Resource.
func CedarPrincipal(p *auth.Principal) *cedar.Principal {
	if p == nil {
		return &cedar.Principal{}
	}
	scopes := make([]string, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		scopes = append(scopes, s.String())
	}
	return &cedar.Principal{
		Subject:    p.Subject,
		TenantID:   p.TenantID,
		TenantSlug: p.TenantSlug,
		Roles:      p.Roles,
		Scopes:     scopes,
	}
}
