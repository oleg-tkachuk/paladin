package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Adversarial tenant-isolation matrix (ADR-0012). Membership keys on the
// DB-authoritative slug (the fake's `slug`) and trusted-UUID equality, so Cedar
// is an INDEPENDENT second isolation layer — a spoofed JWT slug changes
// nothing, and a cross-tenant caller cannot satisfy a member permit even
// knowing the victim's slug.

const bravoMemberPolicy = `
permit (
    principal in Tenant::"bravo",
    action in [Action::"GetObject", Action::"PresignPut"],
    resource
);`

const alphaMemberPolicy = `
permit (
    principal in Tenant::"alpha",
    action in [Action::"GetObject", Action::"PresignPut"],
    resource
);`

// decide evaluates GetObject with an engine whose store returns `policy` and
// the authoritative `authSlug` for EVERY tenant (tenant-agnostic fake — fine,
// because the isolation logic keys on trusted UUID equality, not the fake).
func decide(t *testing.T, policy, authSlug string, p *Principal, r *Resource) Decision {
	t.Helper()
	e := NewEngine(fakeStore{text: policy, slug: authSlug}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(), p, ActionGetObject, r, RequestContext{})
	if err != nil {
		t.Fatalf("authz error: %v", err)
	}
	return dec
}

// Positive control: a legitimate member (own tenant, matching authoritative
// slug) is allowed — proves the deny cases below are real isolation.
func TestAdversarial_LegitMemberAllowed(t *testing.T) {
	b := uuid.New()
	member := &Principal{Subject: "u", TenantID: b, TenantSlug: "bravo", Roles: []string{"tenant.user"}}
	if got := decide(t, bravoMemberPolicy, "bravo", member, &Resource{TenantID: b, Collection: "k"}); got != DecisionAllow {
		t.Fatal("legit member denied on own tenant")
	}
}

// THE core invariant (ADR-0012): a caller in tenant B cannot satisfy tenant A's
// member permit — EVEN when it claims A's slug in its JWT. Cedar denies
// independently of the shim, because membership anchors on the principal's
// trusted UUID, which differs from the resource tenant.
func TestAdversarial_CrossTenantMemberPermitDenied(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	// Attacker in B, claiming A's slug, reaching A's resource.
	attacker := &Principal{Subject: "evil", TenantID: b, TenantSlug: "alpha", Roles: []string{"tenant.admin"}}
	if got := decide(t, alphaMemberPolicy, "alpha", attacker, &Resource{TenantID: a, Collection: "k"}); got != DecisionDeny {
		t.Fatal("SECURITY: cross-tenant caller matched a member permit — tenant isolation hole")
	}
}

// A spoofed tenant_slug is simply IGNORED for the caller's OWN tenant: the
// authoritative slug is used, so the caller keeps its legitimate access and
// gains nothing. (Contrast the reverted JWT-slug design, where a spoof could
// borrow a victim's grants.)
func TestAdversarial_SpoofedSlugIgnoredOnOwnTenant(t *testing.T) {
	b := uuid.New()
	// Real slug is "bravo"; the JWT claims "acme". Accessing own tenant B.
	spoofer := &Principal{Subject: "u", TenantID: b, TenantSlug: "acme", Roles: []string{"tenant.user"}}
	if got := decide(t, bravoMemberPolicy, "bravo", spoofer, &Resource{TenantID: b, Collection: "k"}); got != DecisionAllow {
		t.Fatal("own-tenant access broke when the JWT slug was spoofed — authoritative slug not used")
	}
}

// A tenant-less principal never satisfies a tenant-scoped member permit.
func TestAdversarial_NilTenantPrincipalDenied(t *testing.T) {
	noTenant := &Principal{Subject: "u", Roles: []string{"tenant.user"}}
	if got := decide(t, bravoMemberPolicy, "bravo", noTenant, &Resource{TenantID: uuid.New(), Collection: "k"}); got != DecisionDeny {
		t.Fatal("tenant-less principal matched a tenant-scoped permit")
	}
}

// Cross-tenant reach via a ROLE permit (platform.admin) is preserved — the
// builtin grants by roles, not membership, so legitimate admin flows still
// work under the isolation model.
func TestAdversarial_CrossTenantRolePermitStillAllowed(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	admin := &Principal{Subject: "root", TenantID: b, Roles: []string{"platform.admin"}}
	if got := decide(t, alphaMemberPolicy, "alpha", admin, &Resource{TenantID: a, Collection: "k"}); got != DecisionAllow {
		t.Fatal("platform.admin cross-tenant reach was denied — role permits must not depend on membership")
	}
}
