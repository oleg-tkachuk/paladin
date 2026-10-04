package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Adversarial tenant-isolation matrix (ADR-0016). Membership keys on the
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

// THE core invariant (ADR-0016): a caller in tenant B cannot satisfy tenant A's
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

// The JWT slug is optional during the rollout, and membership deliberately
// anchors on the DB-authoritative slug rather than the claimed one — so a
// principal carrying only its trusted tenant UUID is still a member of that
// tenant. The positive control above sets both fields, which leaves the
// outer membership guard (`p.TenantID != uuid.Nil || p.TenantSlug != ""`)
// unexercised in its first arm.
//
// Requiring both instead of either denies every token minted without a
// tenant_slug claim. That is a deny-where-allow — an outage for those
// callers rather than a hole — and it would present as Cedar refusing
// members access to their own tenant for no visible reason.
func TestAdversarial_MemberWithoutAClaimedSlugIsStillAMember(t *testing.T) {
	b := uuid.New()
	member := &Principal{Subject: "u", TenantID: b, Roles: []string{"tenant.user"}} // no TenantSlug
	if got := decide(t, bravoMemberPolicy, "bravo", member, &Resource{TenantID: b, Collection: "k"}); got != DecisionAllow {
		t.Fatal("a member whose token carries no tenant_slug was denied on its own tenant; " +
			"membership anchors on the authoritative slug, not the claimed one")
	}
}

// The mirror of it: carrying only a slug and no trusted UUID must not confer
// membership anywhere. The UUID is the trusted half, and a token with just a
// string could otherwise claim any tenant it names.
func TestAdversarial_SlugWithoutATrustedUUIDIsNotAMember(t *testing.T) {
	b := uuid.New()
	claimant := &Principal{Subject: "u", TenantSlug: "bravo", Roles: []string{"tenant.user"}} // no TenantID
	if got := decide(t, bravoMemberPolicy, "bravo", claimant, &Resource{TenantID: b, Collection: "k"}); got == DecisionAllow {
		t.Fatal("a principal with no trusted tenant UUID satisfied a member permit by naming the slug")
	}
}

// A platform admin acting on another tenant's data (the data plane's
// acting-tenant path, ADR-0022) is authorised by its role, against that
// tenant's policy set — and is never made a member of it: its principal keeps
// its own tenant, so the target's member permits do not apply to it, and the
// target's forbids still do.
func TestAdversarial_PlatformAdminActingOnAnotherTenant(t *testing.T) {
	platform, target := uuid.New(), uuid.New()
	admin := &Principal{Subject: "admin", TenantID: platform, TenantSlug: "platform", Roles: []string{"platform.admin"}}
	resource := &Resource{TenantID: target, Collection: "k"}

	if got := decide(t, bravoMemberPolicy, "bravo", admin, resource); got != DecisionAllow {
		t.Error("platform admin refused on another tenant: the role permit no longer reaches it")
	}

	const targetForbids = `forbid (principal, action == Action::"GetObject", resource);`
	if got := decide(t, targetForbids, "bravo", admin, resource); got != DecisionDeny {
		t.Error("the target tenant's forbid did not apply to a platform admin acting on it")
	}

	// The same caller without the role is not let in by the target's member
	// permit, as it would be if its principal were pinned to the target.
	plain := &Principal{Subject: "admin", TenantID: platform, TenantSlug: "platform", Roles: []string{"tenant.admin"}}
	if got := decide(t, bravoMemberPolicy, "bravo", plain, resource); got != DecisionDeny {
		t.Fatal("SECURITY: a caller from another tenant matched the target's member permit")
	}
}
