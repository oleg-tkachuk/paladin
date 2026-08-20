package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Behavioural coverage for the reference store (T042). These are the store
// semantics the contract requires; a third party writing their own store can
// read this file as the conformance suite it effectively is.

func mkCap(id, parent uuid.UUID, tenant uuid.UUID, subject string) capability.Capability {
	return capability.Capability{
		ID: id, ParentID: parent,
		Subject:   capability.Principal{Type: capability.PrincipalAgent, TenantID: tenant, Subject: subject},
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// Revocation must be idempotent — an operator retrying a revoke, or two
// operators racing, must not turn the second call into an error.
func TestRevokeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	id := uuid.New()
	if err := s.Insert(ctx, mkCap(id, uuid.Nil, uuid.New(), "a"), capability.Principal{Subject: "test-operator"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.Revoke(ctx, capability.RevokeArgs{ID: id, Reason: "leak", Actor: "op"}); err != nil {
			t.Fatalf("Revoke #%d: %v", i+1, err)
		}
	}
	revoked, err := s.IsRevoked(ctx, id)
	if err != nil || !revoked {
		t.Errorf("IsRevoked = %v, %v; want true, nil", revoked, err)
	}
}

// Cascade is what tears down the sub-agents an orchestrator spawned. Without
// it, revoking the parent leaves its children running with live authority.
func TestRevokeCascadesToDescendants(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	tenant := uuid.New()
	root, child, grandchild := uuid.New(), uuid.New(), uuid.New()

	for _, c := range []capability.Capability{
		mkCap(root, uuid.Nil, tenant, "orchestrator"),
		mkCap(child, root, tenant, "worker"),
		mkCap(grandchild, child, tenant, "sub-worker"),
	} {
		if err := s.Insert(ctx, c, capability.Principal{Subject: "test-operator"}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	if err := s.Revoke(ctx, capability.RevokeArgs{ID: root, CascadeChildren: true}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	for name, id := range map[string]uuid.UUID{"root": root, "child": child, "grandchild": grandchild} {
		revoked, _ := s.IsRevoked(ctx, id)
		if !revoked {
			t.Errorf("%s not revoked — cascade must reach the whole subtree", name)
		}
	}
}

// Without the flag, only the named capability goes. A child keeps working
// until its own TTL expires — that is the documented contract, and quietly
// cascading would be a surprising over-reach.
func TestRevokeWithoutCascadeLeavesChildren(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	tenant := uuid.New()
	root, child := uuid.New(), uuid.New()
	_ = s.Insert(ctx, mkCap(root, uuid.Nil, tenant, "parent"), capability.Principal{Subject: "test-operator"})
	_ = s.Insert(ctx, mkCap(child, root, tenant, "child"), capability.Principal{Subject: "test-operator"})

	if err := s.Revoke(ctx, capability.RevokeArgs{ID: root}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if r, _ := s.IsRevoked(ctx, child); r {
		t.Error("child revoked without CascadeChildren")
	}
}

// An absent record is forgery, not a missing entity (contracts §1.1).
func TestGetAbsentReturnsErrNotFound(t *testing.T) {
	s := New[struct{}]()
	_, err := s.Get(context.Background(), uuid.New())
	if !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("want capability.ErrNotFound, got %v", err)
	}
}

// The ceiling must reject WITHOUT mutating, so a rejected call is safe to
// retry and the counter cannot drift past the limit.
func TestBumpRequestRejectsWithoutMutating(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	id := uuid.New()

	for i := int64(1); i <= 2; i++ {
		n, err := u.BumpRequest(ctx, id, 2)
		if err != nil || n != i {
			t.Fatalf("bump %d: n=%d err=%v", i, n, err)
		}
	}
	n, err := u.BumpRequest(ctx, id, 2)
	if !errors.Is(err, capability.ErrRequestLimitExceeded) {
		t.Fatalf("want ErrRequestLimitExceeded, got %v", err)
	}
	if n != 2 {
		t.Errorf("returned count = %d, want the unchanged 2", n)
	}
	got, _ := u.Get(ctx, id)
	if got.RequestCount != 2 {
		t.Errorf("stored count = %d — a rejected bump mutated state", got.RequestCount)
	}
}

// maxRequests <= 0 means "no ceiling"; it must not be read as "zero allowed".
func TestBumpRequestUnlimited(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	id := uuid.New()
	for i := 0; i < 50; i++ {
		if _, err := u.BumpRequest(ctx, id, 0); err != nil {
			t.Fatalf("unlimited bump %d: %v", i, err)
		}
	}
}

func TestListByPrincipalFilters(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	tenantA, tenantB := uuid.New(), uuid.New()
	idA, idB, revoked := uuid.New(), uuid.New(), uuid.New()

	_ = s.Insert(ctx, mkCap(idA, uuid.Nil, tenantA, "alice"), capability.Principal{Subject: "test-operator"})
	_ = s.Insert(ctx, mkCap(idB, uuid.Nil, tenantB, "bob"), capability.Principal{Subject: "test-operator"})
	_ = s.Insert(ctx, mkCap(revoked, uuid.Nil, tenantA, "carol"), capability.Principal{Subject: "test-operator"})
	_ = s.Revoke(ctx, capability.RevokeArgs{ID: revoked})

	got, _, err := s.ListByPrincipal(ctx, capability.ListByPrincipalArgs{TenantID: tenantA})
	if err != nil {
		t.Fatalf("ListByPrincipal: %v", err)
	}
	if len(got) != 1 || got[0].ID != idA {
		t.Errorf("tenant filter + revoked exclusion failed: %d rows", len(got))
	}

	got, _, _ = s.ListByPrincipal(ctx, capability.ListByPrincipalArgs{
		TenantID: tenantA, IncludeRevoked: true,
	})
	if len(got) != 2 {
		t.Errorf("IncludeRevoked = true returned %d rows, want 2", len(got))
	}
}

// An expired capability is excluded by default — an operator listing live
// authority should not have to filter the dead ones out themselves.
func TestListByPrincipalExcludesExpired(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	tenant := uuid.New()
	id := uuid.New()
	c := mkCap(id, uuid.Nil, tenant, "old")
	c.ExpiresAt = time.Now().Add(-time.Hour)
	_ = s.Insert(ctx, c, capability.Principal{Subject: "test-operator"})

	got, _, _ := s.ListByPrincipal(ctx, capability.ListByPrincipalArgs{TenantID: tenant})
	if len(got) != 0 {
		t.Errorf("expired capability listed by default")
	}
	got, _, _ = s.ListByPrincipal(ctx, capability.ListByPrincipalArgs{
		TenantID: tenant, IncludeExpired: true,
	})
	if len(got) != 1 {
		t.Errorf("IncludeExpired = true returned %d rows, want 1", len(got))
	}
}

// A pre-unit-tracking row must resolve to the default rather than persisting
// an empty unit — clients would otherwise render a bare number.
func TestSetTenantBudgetDefaultsUnitCode(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	b, err := u.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: uuid.New(), MaxBudgetAmount: 10,
	})
	if err != nil {
		t.Fatalf("SetTenantBudget: %v", err)
	}
	if b.UnitCode != capability.DefaultUnitCode {
		t.Errorf("UnitCode = %q, want %q", b.UnitCode, capability.DefaultUnitCode)
	}
}

func TestRefundsFloorAtZero(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	id, tenant := uuid.New(), uuid.New()
	_, _ = u.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{TenantID: tenant, MaxBudgetAmount: 100})
	_, _ = u.Charge(ctx, id, 5, 100, "USD", tenant, "op", "actor", nil)

	// Over-refunding must clamp rather than go negative — a negative counter
	// would read as available budget that was never granted.
	if err := u.RefundCapability(ctx, id, 999); err != nil {
		t.Fatalf("RefundCapability: %v", err)
	}
	if err := u.RefundTenant(ctx, tenant, 999); err != nil {
		t.Fatalf("RefundTenant: %v", err)
	}
	got, _ := u.Get(ctx, id)
	if got.SpentAmount != 0 {
		t.Errorf("capability spend = %v, want floored at 0", got.SpentAmount)
	}
	b, _ := u.GetTenantBudget(ctx, tenant)
	if b.SpentAmount != 0 {
		t.Errorf("tenant spend = %v, want floored at 0", b.SpentAmount)
	}
}
