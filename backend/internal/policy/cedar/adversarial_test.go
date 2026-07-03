package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Adversarial authorization matrix. These are the security invariants the
// slug-inheritance path (own-tenant slug borrow) must never break — a
// mismatched or spoofed slug must FAIL CLOSED and must never let a principal
// reach another tenant's grants. The default policy keys member permits on
// `Tenant::"<slug>"`, so slug handling is the whole ballgame.

// bravoMemberPolicy mimics the rendered default policy for a tenant whose slug
// is "bravo": members may GetObject / PresignPut on their own tenant.
const bravoMemberPolicy = `
permit (
    principal in Tenant::"bravo",
    action in [Action::"GetObject", Action::"PresignPut"],
    resource
);`

// alphaMemberPolicy is the same for tenant "alpha".
const alphaMemberPolicy = `
permit (
    principal in Tenant::"alpha",
    action in [Action::"GetObject", Action::"PresignPut"],
    resource
);`

func decide(t *testing.T, policy string, p *Principal, r *Resource) Decision {
	t.Helper()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(), p, ActionGetObject, r, RequestContext{})
	if err != nil {
		t.Fatalf("authz error: %v", err)
	}
	return dec
}

// A principal that spoofs its OWN slug to a different value must fail closed on
// its own tenant — the policy is keyed on the real slug, the entity graph on
// the spoofed one, so no permit matches. Critically, it gains nothing.
func TestAdversarial_OwnSlugSpoofFailsClosed(t *testing.T) {
	tenantB := uuid.New()
	// JWT claims tenant=B but tenant_slug="acme" (not B's real "bravo" slug).
	spoof := &Principal{Subject: "u", TenantID: tenantB, TenantSlug: "acme", Roles: []string{"tenant.user"}}
	// Accessing B's own resource; B's policy is keyed "bravo".
	if got := decide(t, bravoMemberPolicy, spoof, &Resource{TenantID: tenantB, ObjectKey: "k"}); got != DecisionDeny {
		t.Fatal("slug-spoof on own tenant did not fail closed — a spoofed slug matched a permit")
	}
}

// The decisive cross-tenant case: a principal in tenant B, EVEN KNOWING tenant
// A's real slug and claiming it, must not reach A's resources. r.TenantID (A)
// != p.TenantID (B) ⇒ no slug borrow ⇒ the Tenant entity is A-by-UUID, A's
// slug-keyed permit never matches.
func TestAdversarial_CrossTenantWithVictimSlugDenied(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	attacker := &Principal{Subject: "evil", TenantID: tenantB, TenantSlug: "alpha", Roles: []string{"tenant.admin"}}
	// Attacker reaches for A's resource; the compiled policy is A's (keyed "alpha").
	if got := decide(t, alphaMemberPolicy, attacker, &Resource{TenantID: tenantA, ObjectKey: "k"}); got != DecisionDeny {
		t.Fatal("cross-tenant access with the victim's slug was ALLOWED — tenant isolation hole")
	}
}

// A principal with NO tenant (nil) must never match a tenant-scoped permit.
func TestAdversarial_NilTenantPrincipalDenied(t *testing.T) {
	noTenant := &Principal{Subject: "u", Roles: []string{"tenant.user"}}
	if got := decide(t, bravoMemberPolicy, noTenant, &Resource{TenantID: uuid.New(), ObjectKey: "k"}); got != DecisionDeny {
		t.Fatal("tenant-less principal matched a tenant-scoped permit")
	}
}

// Positive control: a legitimate member (correct slug, own tenant) is allowed —
// proves the deny results above are real isolation, not a policy that denies
// everyone.
func TestAdversarial_LegitMemberAllowed(t *testing.T) {
	tenantB := uuid.New()
	member := &Principal{Subject: "u", TenantID: tenantB, TenantSlug: "bravo", Roles: []string{"tenant.user"}}
	if got := decide(t, bravoMemberPolicy, member, &Resource{TenantID: tenantB, ObjectKey: "k"}); got != DecisionAllow {
		t.Fatal("legitimate member denied on own tenant — the isolation tests would be vacuous")
	}
}

// NOTE (design boundary, verified live + at the e2e layer, not here):
// Cedar does NOT independently enforce cross-tenant isolation. buildEntities
// anchors the User under the RESOURCE's tenant, so a member permit
// `principal in Tenant::"X"` matches whenever the resource carries tenant X's
// slug — regardless of who the caller is. Isolation is enforced BY COMPOSITION
// upstream: the data-plane shim's assertJWTTenant rejects URL-tenant !=
// JWT-tenant for non-admins BEFORE Cedar runs, and compiledFor loads the policy
// by the trusted tenant UUID. The e2e adversarial probe
// (tests/api/security-probe.sh) exercises that boundary against the live API.
// Anchoring membership on the principal's JWT slug to make Cedar a "second
// layer" was tried and reverted: the slug is attacker-controlled, so it let a
// caller claim a victim's slug and match a member permit — strictly worse.
