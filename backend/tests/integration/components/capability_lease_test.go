//go:build integration

package components

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// A lease opened with leasing on, under row-level security as the server
// runs it, holds its amount against the parent, takes the child's charge,
// and settles what it spent to the parent.
func TestLeaseLifecycleUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	usage := newUsageStore(t, rlsPool(t, ctx, f.pool), capstore.WithLeases(true))
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	child := f.subject(t, ctx, f.child)

	lease, err := usage.Lease(ledgerCtx, meter.LeaseRequest{Subject: child, Amount: limes.MustParseAmount("10")})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if root, _ := usage.GetUsage(ledgerCtx, f.root); root.ReservedAmount != limes.MustParseAmount("10") {
		t.Fatalf("parent holds %v under the lease, want 10", root.ReservedAmount)
	}
	if _, err := usage.Charge(ledgerCtx, meter.ChargeRequest{Subject: child, Amount: limes.MustParseAmount("3")}, nil); err != nil {
		t.Fatalf("charge under the lease: %v", err)
	}
	if _, err := usage.Charge(ledgerCtx, meter.ChargeRequest{Subject: child, Amount: limes.MustParseAmount("8")}, nil); !errors.Is(err, meter.ErrLeaseExhausted) {
		t.Fatalf("charge past the lease: err = %v, want ErrLeaseExhausted", err)
	}
	settled, err := usage.SettleLease(ledgerCtx, meter.SettleLeaseRequest{Subject: child})
	if err != nil || settled.ID != lease.ID || settled.Spent != limes.MustParseAmount("3") {
		t.Fatalf("settle = %+v, %v; want lease %s with 3 spent", settled, err, lease.ID)
	}
	root, _ := usage.GetUsage(ledgerCtx, f.root)
	if root.SpentAmount != limes.MustParseAmount("3") || root.ReservedAmount != 0 {
		t.Errorf("parent after settling = %+v, want 3 spent, none held", root)
	}
	if _, err := usage.GetLease(ledgerCtx, f.child); !errors.Is(err, meter.ErrLeaseNotFound) {
		t.Errorf("lease after settling: err = %v, want ErrLeaseNotFound", err)
	}
}

// With leasing off, as during a rollout from a release that knew none, a
// lease is refused and the charge goes through the chain as before.
func TestLeaseRefusedWithLeasingOff(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	_, err := f.usage.Lease(ledgerCtx, meter.LeaseRequest{Subject: f.subject(t, ctx, f.child), Amount: limes.MustParseAmount("1")})
	if !errors.Is(err, meter.ErrLeasingDisabled) {
		t.Fatalf("lease with leasing off: err = %v, want ErrLeasingDisabled", err)
	}
}
