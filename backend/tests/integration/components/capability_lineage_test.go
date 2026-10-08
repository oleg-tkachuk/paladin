//go:build integration

// Delegation trees against a real database. Narrowing at delegation bounds
// each child on its own; these tests pin the two properties that bound the
// tree as a whole: a parent's budget and request ceiling cover everything
// delegated from it, and revoking a parent stops its descendants even
// without CascadeChildren.
package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/capability"
)

type lineageFixture struct {
	pool                 *pgxpool.Pool
	records              *capstore.Store
	usage                *capstore.UsageStore
	tenant               uuid.UUID
	root, child, sibling uuid.UUID
}

// newLineageFixture seeds root → {child, sibling}. The root carries a budget
// of 25 and 3 requests; each child carries 20 and 3, each of which narrows
// the root on its own while the two together do not.
func newLineageFixture(t *testing.T) (context.Context, lineageFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	records := newCapStore(t, pool)

	insert := func(parent uuid.UUID, budget float64, requests int) uuid.UUID {
		c := mkCap(tenant, "agent:"+uuid.NewString()[:8], time.Now().Add(time.Hour))
		c.ParentID = parent
		c.Caveats.MaxBudgetAmount = budget
		c.Caveats.MaxRequests = requests
		if err := records.Insert(ctx, c, seedIssuer); err != nil {
			t.Fatalf("seed capability: %v", err)
		}
		return c.ID
	}
	root := insert(uuid.Nil, 25, 3)
	return ctx, lineageFixture{
		pool:    pool,
		records: records,
		usage:   capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop()),
		tenant:  tenant,
		root:    root,
		child:   insert(root, 20, 3),
		sibling: insert(root, 20, 3),
	}
}

func (f lineageFixture) spent(t *testing.T, ctx context.Context, id uuid.UUID) float64 {
	t.Helper()
	u, err := f.usage.GetUsage(ctx, id)
	if errors.Is(err, capability.ErrUsageNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	return u.SpentAmount
}

func TestChildrenCannotSpendPastTheirParentsBudget(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	charge := func(id uuid.UUID, amount float64) (capability.ChargeReceipt, error) {
		return f.usage.Charge(ctx, capability.ChargeRequest{
			CapabilityID: id, TenantID: f.tenant, Amount: amount, MaxBudget: 20, UnitCode: "USD",
		}, nil)
	}

	first, err := charge(f.child, 20)
	if err != nil {
		t.Fatalf("child within its own and its parent's budget: %v", err)
	}
	if _, err := charge(f.sibling, 20); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("sibling past the parent's ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if got := f.spent(t, ctx, f.sibling); got != 0 {
		t.Errorf("rejected sibling's counter = %v, want 0 (the rollback must cover it)", got)
	}
	if _, err := charge(f.sibling, 5); err != nil {
		t.Fatalf("sibling within what the parent has left: %v", err)
	}
	if got := f.spent(t, ctx, f.root); !closeEnough(got, 25) {
		t.Errorf("parent's subtree spend = %v, want 25", got)
	}

	// Refunding the child's charge returns the room to the parent too.
	if _, err := f.usage.Refund(ctx, capability.RefundRequest{ChargeID: first.ChargeID}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := f.spent(t, ctx, f.root); !closeEnough(got, 5) {
		t.Errorf("parent's subtree spend after refund = %v, want 5", got)
	}
	if _, err := charge(f.sibling, 15); err != nil {
		t.Fatalf("sibling after the refund: %v", err)
	}
}

func TestChildrenCannotExceedTheirParentsRequestCount(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	bump := func(id uuid.UUID) error {
		_, err := f.usage.Bump(ctx, capability.BumpRequest{CapabilityID: id, TenantID: f.tenant, MaxRequests: 3})
		return err
	}
	for i, id := range []uuid.UUID{f.child, f.child, f.sibling} {
		if err := bump(id); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := bump(f.sibling); !errors.Is(err, capability.ErrRequestLimitExceeded) {
		t.Fatalf("fourth request across the subtree: err = %v, want ErrRequestLimitExceeded", err)
	}
	u, err := f.usage.GetUsage(ctx, f.sibling)
	if err != nil {
		t.Fatalf("get sibling usage: %v", err)
	}
	if u.RequestCount != 1 {
		t.Errorf("rejected bump moved the sibling's counter to %d, want 1", u.RequestCount)
	}
}

// Revoking only the root, without CascadeChildren, must still stop the child:
// IsRevoked answers for the chain. Revoking the child leaves the root alone.
func TestRevokingAParentRevokesItsChildrenWithoutCascade(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)

	if err := f.records.Revoke(ctx, capability.RevokeRequest{ID: f.child, Reason: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke child: %v", err)
	}
	if r, err := f.records.IsRevoked(ctx, f.root); err != nil || r {
		t.Fatalf("root after revoking a child: revoked=%v err=%v, want live", r, err)
	}

	if err := f.records.Revoke(ctx, capability.RevokeRequest{ID: f.root, Reason: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke root: %v", err)
	}
	if r, err := f.records.IsRevoked(ctx, f.sibling); err != nil || !r {
		t.Fatalf("sibling after revoking its parent: revoked=%v err=%v, want revoked", r, err)
	}
}

// The same tree under row-level security, the way production runs it: the
// app role without BYPASSRLS, the ledger write scoped to the capability's
// tenant (what the interceptor's ledgerContext does), and the revocation
// check made before any tenant is known. The ancestor walk reads
// capability_records on both paths, so a policy that hid the parent would
// silently turn the subtree ceiling and chain revocation off.
func TestLineageHoldsUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	pool := rlsPool(t, ctx, f.pool)
	usage := capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop())
	records := newCapStore(t, pool)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	receipt, err := usage.Charge(ledgerCtx, capability.ChargeRequest{
		CapabilityID: f.child, TenantID: f.tenant, Amount: 20, MaxBudget: 20, UnitCode: "USD",
	}, nil)
	if err != nil {
		t.Fatalf("child charge under RLS: %v", err)
	}
	if _, err := usage.Charge(ledgerCtx, capability.ChargeRequest{
		CapabilityID: f.sibling, TenantID: f.tenant, Amount: 20, MaxBudget: 20, UnitCode: "USD",
	}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("sibling past the parent's ceiling under RLS: err = %v, want ErrBudgetExceeded", err)
	}

	if got, err := usage.Refund(ledgerCtx, capability.RefundRequest{ChargeID: receipt.ChargeID, Amount: 5}); err != nil || !closeEnough(got, 5) {
		t.Fatalf("refund under RLS = %v, %v; want 5", got, err)
	}
	if got := f.spent(t, ctx, f.root); !closeEnough(got, 15) {
		t.Errorf("parent's subtree spend after an RLS refund = %v, want 15", got)
	}

	if err := f.records.Revoke(ctx, capability.RevokeRequest{ID: f.root, Reason: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke root: %v", err)
	}
	if r, err := records.IsRevoked(ctx, f.child); err != nil || !r {
		t.Fatalf("child after its parent's revoke, under RLS with no tenant: revoked=%v err=%v, want revoked", r, err)
	}
}
