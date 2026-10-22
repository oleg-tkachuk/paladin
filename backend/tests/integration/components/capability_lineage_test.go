//go:build integration

// Delegation trees against a real database. A parent's ceilings covering its
// subtree and a parent's revocation stopping it are limes's rules, which its
// contract suites pin; what Paladin must keep is that they still hold under
// the scope it puts on the application role's transactions.
package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

type lineageFixture struct {
	pool                 *pgxpool.Pool
	records              *pgstore.Store
	usage                *capstore.UsageStore
	tenant               uuid.UUID
	root, child, sibling uuid.UUID
	// expires is when every capability of the tree expires: a child may not
	// outlive its parent.
	expires time.Time
}

// The lineage fixture's ceilings: the root's budget and request count, and
// each child's, which narrows the root's on its own while the two children
// together do not. A lineage counts in one unit.
var (
	lineageRootBudget  = limes.BudgetCeiling{Max: limes.MustParseAmount("25"), UnitCode: testUnit}
	lineageChildBudget = limes.BudgetCeiling{Max: limes.MustParseAmount("20"), UnitCode: testUnit}
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
		usage:   newUsageStore(t, pool),
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
	if errors.Is(err, meter.ErrUsageNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	return u.SpentAmount
}

// The same tree under row-level security, the way production runs it: the
// app role without BYPASSRLS, the ledger write scoped to the capability's
// tenant (what the interceptor's ledgerContext does), and the revocation
// check made before any tenant is known. The ancestor walk reads
// limes.capabilities on both paths, so a scope that hid the parent would
// silently turn the subtree ceiling and chain revocation off.
func TestLineageHoldsUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	pool := rlsPool(t, ctx, f.pool)
	usage := newUsageStore(t, pool)
	records := newCapStore(t, pool)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	receipt, err := usage.Charge(ledgerCtx, meter.ChargeRequest{
		Subject: f.subject(t, ctx, f.child), Amount: limes.MustParseAmount("20"),
	}, nil)
	if err != nil {
		t.Fatalf("child charge under RLS: %v", err)
	}
	if _, err := usage.Charge(ledgerCtx, meter.ChargeRequest{
		Subject: f.subject(t, ctx, f.sibling), Amount: limes.MustParseAmount("20"),
	}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("sibling past the parent's ceiling under RLS: err = %v, want ErrBudgetExceeded", err)
	}

	if got, err := usage.Refund(ledgerCtx, meter.RefundRequest{ChargeID: receipt.ChargeID, Amount: limes.MustParseAmount("5")}); err != nil || got.Amount != limes.MustParseAmount("5") {
		t.Fatalf("refund under RLS = %v, %v; want 5", got, err)
	}
	if got := f.spent(t, ctx, f.root); got != limes.MustParseAmount("15") {
		t.Errorf("parent's subtree spend after an RLS refund = %v, want 15", got)
	}

	if _, err := f.records.Revoke(ctx, limes.RevokeRequest{CapabilityID: f.root, Note: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke root: %v", err)
	}
	if r, err := records.IsRevoked(ctx, f.child); err != nil || !r {
		t.Fatalf("child after its parent's revoke, under RLS with no tenant: revoked=%v err=%v, want revoked", r, err)
	}
}
