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

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type lineageFixture struct {
	pool                 *pgxpool.Pool
	records              *capstore.Store
	usage                *capstore.UsageStore
	tenant               uuid.UUID
	root, child, sibling uuid.UUID
	// expires is when every capability of the tree expires: a child may not
	// outlive its parent.
	expires time.Time
}

// The lineage fixture's ceilings: the root's budget and request count, and
// each child's, which narrows the root's on its own while the two children
// together do not.
var (
	lineageRootBudget  = limes.BudgetCeiling{Max: limes.MustParseAmount("25")}
	lineageChildBudget = limes.BudgetCeiling{Max: limes.MustParseAmount("20")}
	lineageRequests    = limes.RequestCeiling{Max: 3}
)

// newLineageFixture seeds root → {child, sibling}. The root carries a budget
// of 25 and 3 requests; each child carries 20 and 3, each of which narrows
// the root on its own while the two together do not.
func newLineageFixture(t *testing.T) (context.Context, lineageFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	f := lineageFixture{
		pool:    pool,
		records: newCapStore(t, pool),
		usage:   capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop()),
		tenant:  tenant,
		expires: time.Now().Add(time.Hour),
	}
	f.root = f.insert(t, ctx, uuid.Nil, lineageRootBudget, lineageRequests)
	f.child = f.insert(t, ctx, f.root, lineageChildBudget, lineageRequests)
	f.sibling = f.insert(t, ctx, f.root, lineageChildBudget, lineageRequests)
	return ctx, f
}

// insert puts a capability of the fixture's tenant on record under parent
// (none when uuid.Nil) with the given ceilings, and returns its id.
func (f lineageFixture) insert(t *testing.T, ctx context.Context, parent uuid.UUID, budget limes.BudgetCeiling, requests limes.RequestCeiling) uuid.UUID {
	t.Helper()
	c := mkCap(f.tenant, "agent:"+uuid.NewString()[:8], f.expires)
	c.ParentID = parent
	c.Caveats.Budget = budget
	c.Caveats.Requests = requests
	if err := f.records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return c.ID
}

// subject is the meter subject of a capability of the fixture, as on record.
func (f lineageFixture) subject(t *testing.T, ctx context.Context, id uuid.UUID) limes.MeterSubject {
	t.Helper()
	return storedSubject(t, ctx, f.records, id)
}

func (f lineageFixture) spent(t *testing.T, ctx context.Context, id uuid.UUID) limes.Nanos {
	t.Helper()
	u, err := f.usage.GetUsage(ctx, id)
	if errors.Is(err, limes.ErrUsageNotFound) {
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
	charge := func(id uuid.UUID, amount string) (limes.ChargeReceipt, error) {
		return f.usage.Charge(ctx, limes.ChargeRequest{
			Subject: f.subject(t, ctx, id), Amount: limes.MustParseAmount(amount),
		}, nil)
	}

	first, err := charge(f.child, "20")
	if err != nil {
		t.Fatalf("child within its own and its parent's budget: %v", err)
	}
	if _, err := charge(f.sibling, "20"); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("sibling past the parent's ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if got := f.spent(t, ctx, f.sibling); got != 0 {
		t.Errorf("rejected sibling's counter = %v, want 0 (the rollback must cover it)", got)
	}
	if _, err := charge(f.sibling, "5"); err != nil {
		t.Fatalf("sibling within what the parent has left: %v", err)
	}
	if got := f.spent(t, ctx, f.root); got != limes.MustParseAmount("25") {
		t.Errorf("parent's subtree spend = %v, want 25", got)
	}

	// Refunding the child's charge returns the room to the parent too.
	if _, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: first.ChargeID, Remaining: true}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := f.spent(t, ctx, f.root); got != limes.MustParseAmount("5") {
		t.Errorf("parent's subtree spend after refund = %v, want 5", got)
	}
	if _, err := charge(f.sibling, "15"); err != nil {
		t.Fatalf("sibling after the refund: %v", err)
	}
}

func TestChildrenCannotExceedTheirParentsRequestCount(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	bump := func(id uuid.UUID) error {
		_, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: f.subject(t, ctx, id)})
		return err
	}
	for i, id := range []uuid.UUID{f.child, f.child, f.sibling} {
		if err := bump(id); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := bump(f.sibling); !errors.Is(err, limes.ErrRequestLimitExceeded) {
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

	if err := f.records.Revoke(ctx, limes.RevokeRequest{CapabilityID: f.child, Note: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke child: %v", err)
	}
	if r, err := f.records.IsRevoked(ctx, f.root); err != nil || r {
		t.Fatalf("root after revoking a child: revoked=%v err=%v, want live", r, err)
	}

	if err := f.records.Revoke(ctx, limes.RevokeRequest{CapabilityID: f.root, Note: "test", Actor: "user:ops"}); err != nil {
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

	receipt, err := usage.Charge(ledgerCtx, limes.ChargeRequest{
		Subject: f.subject(t, ctx, f.child), Amount: limes.MustParseAmount("20"),
	}, nil)
	if err != nil {
		t.Fatalf("child charge under RLS: %v", err)
	}
	if _, err := usage.Charge(ledgerCtx, limes.ChargeRequest{
		Subject: f.subject(t, ctx, f.sibling), Amount: limes.MustParseAmount("20"),
	}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("sibling past the parent's ceiling under RLS: err = %v, want ErrBudgetExceeded", err)
	}

	if got, err := usage.Refund(ledgerCtx, limes.RefundRequest{ChargeID: receipt.ChargeID, Amount: limes.MustParseAmount("5")}); err != nil || got.Amount != limes.MustParseAmount("5") {
		t.Fatalf("refund under RLS = %v, %v; want 5", got, err)
	}
	if got := f.spent(t, ctx, f.root); got != limes.MustParseAmount("15") {
		t.Errorf("parent's subtree spend after an RLS refund = %v, want 15", got)
	}

	if err := f.records.Revoke(ctx, limes.RevokeRequest{CapabilityID: f.root, Note: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke root: %v", err)
	}
	if r, err := records.IsRevoked(ctx, f.child); err != nil || !r {
		t.Fatalf("child after its parent's revoke, under RLS with no tenant: revoked=%v err=%v, want revoked", r, err)
	}
}
