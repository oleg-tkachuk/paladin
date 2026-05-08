package apiutil

import (
	"errors"
	"regexp"
)

// tenantSlugRE mirrors the Postgres CHECK constraint
// `tenants_slug_format` in migrations/009_tenant_slug.sql. Keep both in sync.
//
// Format:
//   - 3..63 chars
//   - first char: lowercase letter
//   - middle chars: lowercase alnum or '-'
//   - last char: lowercase alnum
//
// Picked to be DNS-label-compatible so a slug can later be used as a
// hostname/subdomain prefix without escaping.
var tenantSlugRE = regexp.MustCompile(`^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$`)

// ValidateTenantSlug returns nil iff `s` is a syntactically valid tenant
// slug. Uniqueness is enforced by the database, not here.
func ValidateTenantSlug(s string) error {
	if s == "" {
		return errors.New("tenant slug must not be empty")
	}
	if len(s) < 3 || len(s) > 63 {
		return errors.New("tenant slug must be 3..63 chars")
	}
	if !tenantSlugRE.MatchString(s) {
		return errors.New("tenant slug must match ^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$ (kebab-case)")
	}
	return nil
}

// IsTenantSlug reports whether `s` looks like a slug (vs. a UUID). The
// resource-name parser uses this to dispatch between slug-lookup and UUID
// parsing without a database round-trip.
func IsTenantSlug(s string) bool {
	return tenantSlugRE.MatchString(s)
}
