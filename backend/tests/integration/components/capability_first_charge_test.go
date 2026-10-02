//go:build integration

package components

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/capability"
)

// A first charge used to skip the ceiling: the INSERT path of the upsert
// carried no check, so a capability capped at 10 accepted 50 the first time
// it was charged — and so did an ancestor whose usage row did not exist yet.
func TestFirstChargeRespectsTheCeiling(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)
	if _, err := f.usage.Charge(ctx, capability.ChargeRequest{
		CapabilityID: f.capID, TenantID: f.tenant, Amount: 50, MaxBudget: 10, UnitCode: "USD",
	}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
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
	// The child's own ceiling is 20; the parent's 25. The parent has never
	// been charged, so its usage row does not exist yet. 30 fits neither.
	if _, err := f.usage.Charge(ctx, capability.ChargeRequest{
		CapabilityID: f.child, TenantID: f.tenant, Amount: 30, MaxBudget: 0, UnitCode: "USD",
	}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("first charge above the parent's ceiling: err = %v, want ErrBudgetExceeded", err)
	}
}
