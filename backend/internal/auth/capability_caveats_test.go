package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
)

// usageStore is the reference Meter the caveat tests charge against, typed
// with the transaction handle the interceptor's store takes.
type usageStore = memstore.UsageStore

// newMeteredMinter is a Minter and a usage store over its records. A Meter
// refuses a capability it cannot find on record, so every capability a test
// meters is minted by this Minter.
func newMeteredMinter(t *testing.T) (*captest.Minter, *usageStore) {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	return m, usage
}

// budgeted is a capability template with a budget ceiling of ceiling.
func budgeted(ceiling limes.Nanos) limes.Capability {
	return limes.Capability{Caveats: limes.Caveats{Budget: limes.BudgetCeiling{Max: ceiling, UnitCode: captest.Unit}}}
}

// spentOf is the capability's recorded spend; zero when nothing was recorded.
func spentOf(t *testing.T, usage *usageStore, v limes.VerifiedCapability) limes.Nanos {
	t.Helper()
	u, err := usage.GetUsage(context.Background(), v.Capability().ID)
	if errors.Is(err, meter.ErrUsageNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	return u.SpentAmount
}

var (
	oneUnit     = limes.MustParseAmount("1.0")
	halfUnit    = limes.MustParseAmount("0.5")
	tenthOfUnit = limes.MustParseAmount("0.10")
)

// ─── ChargeCapability ───────────────────────────────────────────────

func TestChargeCapability_NoCapability_NoOp(t *testing.T) {
	if err := ChargeCapability(context.Background(), oneUnit); err != nil {
		t.Errorf("no capability + Charge must be no-op, got %v", err)
	}
}

func TestChargeCapability_NoStoreInContext_NoOp(t *testing.T) {
	ctx := WithCapability(context.Background(), captest.Verified(t, budgeted(oneUnit)))
	if err := ChargeCapability(ctx, halfUnit); err != nil {
		t.Errorf("no store on context = no-op, got %v", err)
	}
}

func TestChargeCapability_RecordsAndAllows(t *testing.T) {
	charge := limes.MustParseAmount("0.30")
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, budgeted(oneUnit))
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	if err := ChargeCapability(ctx, charge); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if err := ChargeCapability(ctx, charge); err != nil {
		t.Fatalf("second charge: %v", err)
	}
	if got, want := spentOf(t, usage, v), 2*charge; got != want {
		t.Errorf("spent = %v, want %v", got, want)
	}
}

func TestChargeCapability_OverBudget_Rejects(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, budgeted(oneUnit))
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	if err := ChargeCapability(ctx, limes.MustParseAmount("0.80")); err != nil {
		t.Fatalf("0.80 charge: %v", err)
	}
	err := ChargeCapability(ctx, halfUnit) // would exceed 1.00
	if err == nil {
		t.Fatal("expected over-budget error")
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeResourceExhausted {
		t.Errorf("expected CodeResourceExhausted, got %v", err)
	}
}

func TestChargeCapability_UnlimitedBudget_NeverRejects(t *testing.T) {
	const charges = 100
	charge := limes.MustParseAmount("100.0")
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: captest.Unit}}})
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	for i := range charges {
		if err := ChargeCapability(ctx, charge); err != nil {
			t.Fatalf("unlimited budget should never reject, got %v at i=%d", err, i)
		}
	}
}

// A budget ceiling of zero allows no spend at all. It used to mean
// "unlimited", so a capability minted to spend nothing could spend anything.
func TestChargeCapability_ZeroBudget_RefusesAnyCharge(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.MintExact(t, limes.Capability{Caveats: limes.Caveats{
		Requests: limes.RequestCeiling{Unlimited: true},
		Budget:   limes.BudgetCeiling{UnitCode: captest.Unit},
	}})
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	err := ChargeCapability(ctx, tenthOfUnit)
	if connect.CodeOf(err) != connect.CodeResourceExhausted || !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("charge under a zero budget: err = %v, want ResourceExhausted wrapping ErrBudgetExceeded", err)
	}
	if got := spentOf(t, usage, v); got != 0 {
		t.Errorf("a refused charge recorded spend %v", got)
	}
}

func TestChargeCapability_NegativeOrZero_NoOp(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, budgeted(oneUnit))
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	if err := ChargeCapability(ctx, 0); err != nil {
		t.Errorf("zero charge must be no-op, got %v", err)
	}
	if err := ChargeCapability(ctx, -limes.NanosPerUnit); err != nil {
		t.Errorf("negative charge must be no-op, got %v", err)
	}
	if got := spentOf(t, usage, v); got != 0 {
		t.Errorf("spent should be 0, got %v", got)
	}
}

// A charge is always in the capability's own unit, which its token pins: the
// caller names only an amount, and the ledger files it under that unit.
func TestChargeCapability_ChargesInTheCapabilitysUnit(t *testing.T) {
	const unit = "EUR"
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{
		Budget: limes.BudgetCeiling{Max: oneUnit, UnitCode: unit},
	}})
	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), v), usage))

	if err := ChargeCapability(ctx, halfUnit); err != nil {
		t.Fatalf("charge: %v", err)
	}
	rec, err := usage.GetCharge(ctx, readLastCharge(ctx))
	if err != nil {
		t.Fatalf("the charge is not in the ledger: %v", err)
	}
	if rec.UnitCode != unit || rec.Amount != halfUnit {
		t.Errorf("ledger row = %v %s, want %v %s", rec.Amount, rec.UnitCode, halfUnit, unit)
	}
	u, err := usage.GetUsage(ctx, v.Capability().ID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.UnitCode != unit {
		t.Errorf("usage unit = %q, want the capability's %q", u.UnitCode, unit)
	}
}

// ─── ChargeRequest ──────────────────────────────────────────────────

func TestChargeRequest_NoAmountInContext_NoOp(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{})
	// Cap + store but NO amount on context — handler shouldn't be
	// charged because cfg.ChargePerRequest defaulted to 0.
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	if err := ChargeRequest(ctx); err != nil {
		t.Errorf("no amount stamped = no-op, got %v", err)
	}
	if got := spentOf(t, usage, v); got != 0 {
		t.Errorf("spent should stay 0, got %v", got)
	}
}

func TestChargeRequest_AmountPresent_Charges(t *testing.T) {
	perRequest := limes.MustParseAmount("0.001")
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{})
	ctx := WithChargeAmount(WithChargeStore(WithCapability(context.Background(), v), usage), perRequest)
	if err := ChargeRequest(ctx); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if got := spentOf(t, usage, v); got != perRequest {
		t.Errorf("spent = %v, want %v", got, perRequest)
	}
}

func TestChargeRequest_NoCapability_NoOp(t *testing.T) {
	// No capability on context — JWT auth path. Even with an amount
	// stamped, ChargeRequest returns nil without touching the store.
	_, usage := newMeteredMinter(t)
	ctx := WithChargeAmount(WithChargeStore(context.Background(), usage), halfUnit)
	if err := ChargeRequest(ctx); err != nil {
		t.Errorf("no capability = no-op, got %v", err)
	}
}

// ─── RefundLastCharge ───────────────────────────────────────────────

// chargedContext is a request context as the interceptor builds it: the
// per-request holders installed, so a charge is remembered for refunding.
func chargedContext(t *testing.T, v limes.VerifiedCapability, usage *usageStore, amount limes.Nanos) context.Context {
	t.Helper()
	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), v), usage))
	if err := ChargeCapability(ctx, amount); err != nil {
		t.Fatalf("charge: %v", err)
	}
	return ctx
}

func TestRefundLastCharge_PartialThenRest(t *testing.T) {
	charge, partial := halfUnit, limes.MustParseAmount("0.30")
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, budgeted(oneUnit))
	ctx := chargedContext(t, v, usage, charge)

	if err := RefundLastCharge(ctx, partial); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if got, want := spentOf(t, usage, v), charge-partial; got != want {
		t.Errorf("spent after partial refund = %v, want %v", got, want)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("refund of the rest: %v", err)
	}
	if got := spentOf(t, usage, v); got != 0 {
		t.Errorf("spent after full refund = %v, want 0", got)
	}
}

// A refund is tied to the charge it returns, so it can never give back more
// than was taken — the old counter decrement was clamped at zero, but a
// repeated refund still credited spend that other charges had made.
func TestRefundLastCharge_CannotExceedTheCharge(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{})
	ctx := chargedContext(t, v, usage, tenthOfUnit)

	err := RefundLastCharge(ctx, oneUnit)
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, meter.ErrRefundExceedsCharge) {
		t.Fatalf("over-refund err = %v, want InvalidArgument wrapping ErrRefundExceedsCharge", err)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("full refund: %v", err)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("repeated full refund must be a no-op, got %v", err)
	}
}

func TestRefundLastCharge_NoCapNoStoreNoCharge_NoOp(t *testing.T) {
	if err := RefundLastCharge(context.Background(), oneUnit); err != nil {
		t.Errorf("no cap: %v", err)
	}
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{})
	ctx := WithCapability(context.Background(), v)
	if err := RefundLastCharge(ctx, oneUnit); err != nil {
		t.Errorf("no store: %v", err)
	}
	ctx = withLastOpHolder(WithChargeStore(ctx, usage))
	if err := RefundLastCharge(ctx, oneUnit); err != nil {
		t.Errorf("no charge yet: %v", err)
	}
}
