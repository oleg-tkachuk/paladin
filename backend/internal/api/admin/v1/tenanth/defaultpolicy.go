package tenanth

import (
	"strings"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/policies"
)

// renderDefaultPolicy returns policies.DefaultTenantPolicy, the policy a new
// tenant gets when the caller supplies none, with the placeholder Tenant UID
// replaced by the tenant's slug (the canonical Cedar Tenant UID — see
// internal/policy/cedar/engine.go:tenantUID). Falls back to the UUID when
// slug is empty (legacy callers still constructing tenants without a slug).
func renderDefaultPolicy(tenantID uuid.UUID, slug string) string {
	uid := slug
	if uid == "" {
		uid = tenantID.String()
	}
	return strings.ReplaceAll(policies.DefaultTenantPolicy, policies.TenantPlaceholder, uid)
}
