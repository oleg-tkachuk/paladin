//go:build integration

package components

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// A first charge used to skip the ceiling: the INSERT path of the upsert
// carried no check, so a capability capped at 10 accepted 50 the first time
// it was charged — and so did an ancestor whose usage row did not exist yet.
func TestFirstChargeRespectsTheCeiling(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixtureWith(t, func(c *limes.Caveats) { c.Budget = limes.BudgetCeiling{Max: limes.MustParseAmount("10")} })
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{
		Subject: f.subject, Amount: limes.MustParseAmount("50"),
	}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("first charge above the cap: err = %v, want ErrBudgetExceeded", err)
	}
	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter %v after a refused first charge, want 0", got)
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("ledger has %d rows after a refused first charge, want 0", n)
	}
}

func TestFirstChargeOfAnAncestorRespectsItsCeiling(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	// A child of the root with no budget of its own, so only the root's 25
	// can refuse. The root has never been charged, so its usage row does not
	// exist yet. 30 does not fit it.
	child := f.insert(t, ctx, f.root, limes.BudgetCeiling{Unlimited: true}, limes.RequestCeiling{Unlimited: true})
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{
		Subject: f.subject(t, ctx, child), Amount: limes.MustParseAmount("30"),
	}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("first charge above the parent's ceiling: err = %v, want ErrBudgetExceeded", err)
	}
}
