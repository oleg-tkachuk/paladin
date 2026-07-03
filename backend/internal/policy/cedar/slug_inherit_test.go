package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The rendered default policy keys its member permits on `Tenant::"<slug>"`,
// but most data-plane call sites build the Resource with only the tenant
// UUID — the entity graph then keys the Tenant by UUID and the slug-keyed
// group permit never matches, denying every tenant MEMBER (only
// platform.admin survives via the builtin; confirmed live on the deployed
// cluster). IsAuthorized now inherits the principal's slug when the resource
// targets the principal's own tenant.
func TestIsAuthorized_InheritsPrincipalSlugForOwnTenant(t *testing.T) {
	const memberPermit = `
permit (
    principal in Tenant::"acme",
    action in [Action::"GetObject", Action::"PresignPut"],
    resource
);`
	e := NewEngine(fakeStore{text: memberPermit}, time.Minute)
	own := uuid.New()
	member := &Principal{Subject: "u@acme", TenantID: own, TenantSlug: "acme", Roles: []string{"tenant.user"}}

	// Resource carries only the UUID — the exact shape the data-plane
	// handlers build. Must ALLOW via the inherited slug.
	dec, err := e.IsAuthorized(context.Background(), member, ActionGetObject,
		&Resource{TenantID: own, ObjectKey: "docs", Key: "hello.txt"}, RequestContext{})
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if dec != DecisionAllow {
		t.Fatal("member denied on own tenant with UUID-only resource — slug inheritance broken")
	}

	// A principal WITHOUT a slug (legacy JWT) must stay on the UUID path —
	// the slug-keyed permit doesn't match, and that's the pre-existing
	// legacy behaviour, not a regression.
	noSlug := &Principal{Subject: "u@acme", TenantID: own, Roles: []string{"tenant.user"}}
	dec, err = e.IsAuthorized(context.Background(), noSlug, ActionGetObject,
		&Resource{TenantID: own, ObjectKey: "docs", Key: "hello.txt"}, RequestContext{})
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatal("slug-less principal unexpectedly matched a slug-keyed permit")
	}
}

// A cross-tenant resource must never borrow the caller's slug: the foreign
// tenant's identity is its own, and inheriting the caller's slug would make
// `principal in Tenant::"acme"` match against a FOREIGN tenant's resource.
func TestIsAuthorized_NoSlugBorrowAcrossTenants(t *testing.T) {
	const memberPermit = `
permit (
    principal in Tenant::"acme",
    action == Action::"GetObject",
    resource
);`
	e := NewEngine(fakeStore{text: memberPermit}, time.Minute)
	own, foreign := uuid.New(), uuid.New()
	member := &Principal{Subject: "u@acme", TenantID: own, TenantSlug: "acme", Roles: []string{"tenant.user"}}

	dec, err := e.IsAuthorized(context.Background(), member, ActionGetObject,
		&Resource{TenantID: foreign, ObjectKey: "docs", Key: "x"}, RequestContext{})
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatal("cross-tenant resource borrowed the caller's slug — tenant isolation hole")
	}
}
