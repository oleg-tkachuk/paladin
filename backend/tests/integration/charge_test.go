//go:build integration

// Two-phase capability charge against a real Postgres. Validates the
// compensation logic in capability/postgres/usage.go: a tenant-cap
// rejection that arrives AFTER a successful per-cap charge must
// undo the per-cap counter so the two stay in sync.
package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// TestCharge_TwoPhase_TenantCapCompensatesCapability:
//  1. Cap budget = 10, tenant budget = 5. Charge 4 → both fit, success.
//  2. Charge another 4 → cap-side accepts (8 ≤ 10), tenant-side
//     rejects (8 > 5). Inner store must compensate: cap counter
//     reverts to 4.
//  3. Get final state via UsageStore.GetUsage + GetTenantBudget.
func TestCharge_TwoPhase_TenantCapCompensatesCapability(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	// The BYPASSRLS migrate pool, so the calls need no tenant in context.
	// The ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-charge")
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, limes.BudgetCeiling{Max: limes.MustParseAmount("10.0")})

	// First charge: 4. Cap allows 10, tenant allows 5. Both fit.
	spent, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("4.0")}, nil)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if spent.SpentAmount != limes.MustParseAmount("4") {
		t.Errorf("spent after first charge = %v, want 4.00", spent.SpentAmount)
	}

	// Configure a tenant cap of 5 (a ceiling never set is unlimited; we
	// want the rejection path).
	//
	// The charge above already created the accumulator row; it holds only
	// counted spend, so its version is still 0 — the read-then-write an
	// operator does from the console carries exactly that.
	cur, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("read tenant budget: %v", err)
	}
	if _, err := store.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID:        tenantID,
		Ceiling:         limes.BudgetCeiling{Max: limes.MustParseAmount("5.0")},
		ResetSpend:      false, // keep the 4 we already charged
		ExpectedVersion: cur.ResourceVersion,
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}

	// Second charge: 4. Cap accepts (8 ≤ 10). Tenant rejects (8 > 5).
	// Inner store should compensate the per-cap counter.
	_, err = store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("4.0")}, nil)
	if !errors.Is(err, limes.ErrTenantBudgetExceeded) {
		t.Fatalf("second charge: want ErrTenantBudgetExceeded, got %v", err)
	}

	// Verify the per-capability counter is back at 4 (not 8).
	usage, err := store.GetUsage(ctx, capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.SpentAmount != limes.MustParseAmount("4") {
		t.Errorf("post-rejection cap spent = %v, want 4.00 (compensation didn't fire)", usage.SpentAmount)
	}

	// Verify the tenant counter is at 4 too — the rejected charge
	// never went through.
	tb, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("get tenant budget: %v", err)
	}
	if tb.SpentAmount != limes.MustParseAmount("4") {
		t.Errorf("tenant spent = %v, want 4.00", tb.SpentAmount)
	}
}

// TestCharge_RefundReturnsExactlyTheCharge: a refund larger than its charge
// is refused outright, and a full refund returns both counters to where they
// were — never below, which would grant the difference back as future budget.
func TestCharge_RefundReturnsExactlyTheCharge(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	// The ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-refund")
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, limes.BudgetCeiling{Unlimited: true})

	receipt, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("1.0")}, nil)
	if err != nil {
		t.Fatalf("seed charge: %v", err)
	}
	// Refund more than was charged — refused, and nothing moves.
	if _, err := store.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Amount: limes.MustParseAmount("5.0")}); !errors.Is(err, limes.ErrRefundExceedsCharge) {
		t.Fatalf("over-refund err = %v, want ErrRefundExceedsCharge", err)
	}
	// Refund the whole charge — both counters back to 0.
	if _, err := store.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Remaining: true}); err != nil {
		t.Fatalf("refund: %v", err)
	}

	u, err := store.GetUsage(ctx, capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if u.SpentAmount != 0 {
		t.Errorf("cap spent after over-refund = %v, want 0", u.SpentAmount)
	}
	tb, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tb.SpentAmount != 0 {
		t.Errorf("tenant spent after over-refund = %v, want 0", tb.SpentAmount)
	}
}

// TestCharge_PeriodRollResetsSpend: SetTenantBudget(reset_spend=true)
// rolls the period — spent_usd back to 0, period_start to now.
func TestCharge_PeriodRollResetsSpend(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	// The ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-period")

	// Initial cap 10, charge 7 against an unrelated capability.
	initial, err := store.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID:   tenantID,
		Ceiling:    limes.BudgetCeiling{Max: limes.MustParseAmount("10.0")},
		ResetSpend: true,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, limes.BudgetCeiling{Unlimited: true})
	if _, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("7.0")}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	tb, _ := store.GetTenantBudget(ctx, tenantID)
	if tb.SpentAmount != limes.MustParseAmount("7") {
		t.Fatalf("pre-roll spent = %v, want 7", tb.SpentAmount)
	}

	// Roll the period: same cap (10), reset_spend=true. The roll carries the
	// version the create returned — Charge() does not bump it, so the caller's
	// read is still current.
	if _, err := store.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID:        tenantID,
		Ceiling:         limes.BudgetCeiling{Max: limes.MustParseAmount("10.0")},
		ResetSpend:      true,
		ExpectedVersion: initial.ResourceVersion,
	}); err != nil {
		t.Fatalf("roll: %v", err)
	}
	tb, _ = store.GetTenantBudget(ctx, tenantID)
	if tb.SpentAmount != 0 {
		t.Errorf("post-roll spent = %v, want 0", tb.SpentAmount)
	}
}

// TestCharge_LedgerRowAppearsAfterCharge: a successful Charge() must
// also write a row into the charges ledger (the schema baseline (001_initial_schema.sql)) carrying
// the same amount + unit + tenant + op + actor. The ledger backs the
// BillingService time-series surface.
func TestCharge_LedgerRowAppearsAfterCharge(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-ledger")
	capID := uuid.New()

	// The meter refuses a capability not on record.
	subject := seedCapRecord(t, h, capID, tenantID, limes.BudgetCeiling{Max: limes.MustParseAmount("10.0")})

	if _, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("2.5"), Op: "presign.put", Actor: "agent-1"}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	var (
		count    int64
		amount   limes.Nanos
		unitCode string
		op       string
		actor    string
	)
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount), 0)::bigint,
		        COALESCE((SELECT unit_code FROM limes.charges WHERE tenant_id = $1 LIMIT 1), ''),
		        COALESCE((SELECT op FROM limes.charges WHERE tenant_id = $1 LIMIT 1), ''),
		        COALESCE((SELECT actor FROM limes.charges WHERE tenant_id = $1 LIMIT 1), '')
		 FROM limes.charges WHERE tenant_id = $1`, tenantID,
	).Scan(&count, &amount, &unitCode, &op, &actor); err != nil {
		t.Fatalf("ledger probe: %v", err)
	}
	if count != 1 {
		t.Errorf("ledger rows = %d, want 1", count)
	}
	if amount != limes.MustParseAmount("2.5") {
		t.Errorf("ledger amount = %v, want 2.50", amount)
	}
	if unitCode != "USD" || op != "presign.put" || actor != "agent-1" {
		t.Errorf("ledger metadata = (%q, %q, %q), want (USD, presign.put, agent-1)",
			unitCode, op, actor)
	}
}

// TestCharge_RefusesCapabilityNoLongerOnRecord: a subject read while its
// capability was on record is refused once the record is gone, and nothing is
// counted — metering it alone would let it spend past every ancestor's
// ceiling. The ledger rows it already wrote outlive the record.
func TestCharge_RefusesCapabilityNoLongerOnRecord(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-gone")
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, limes.BudgetCeiling{Unlimited: true})
	if _, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("1.0")}, nil); err != nil {
		t.Fatalf("charge on record: %v", err)
	}
	if _, err := h.PoolMigrate.Exec(ctx, `DELETE FROM limes.capabilities WHERE id = $1`, capID); err != nil {
		t.Fatalf("drop record: %v", err)
	}

	if _, err := store.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("1.0")}, nil); !errors.Is(err, limes.ErrNotFound) {
		t.Fatalf("charge off record: err = %v, want ErrNotFound", err)
	}
	if got := countCharges(t, h, tenantID); got != 1 {
		t.Errorf("charges rows = %d, want 1 (the first charge's row outlives its record; the refused one wrote none)", got)
	}
	tb, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("get tenant budget: %v", err)
	}
	if tb.SpentAmount != limes.MustParseAmount("1.0") {
		t.Errorf("tenant spent = %v, want 1.00 (the refused charge must not count)", tb.SpentAmount)
	}
}
