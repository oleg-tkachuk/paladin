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

	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	"github.com/oleg-tkachuk/paladin/capability"
)

// TestCharge_TwoPhase_TenantCapCompensatesCapability:
//  1. Cap budget = 10, tenant budget = 5. Charge 4 → both fit, success.
//  2. Charge another 4 → cap-side accepts (8 ≤ 10), tenant-side
//     rejects (8 > 5). Inner store must compensate: cap counter
//     reverts to 4.
//  3. Get final state via UsageStore.Get + GetTenantBudget.
func TestCharge_TwoPhase_TenantCapCompensatesCapability(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	// Use the BYPASSRLS migrate pool so we don't have to thread
	// tenant context for capability_records seeding.
	q := sqlc.New(h.PoolMigrate)
	// Real pool: the store requires one (a nil pool used to mean "ledger
	// insert disabled", which made Charge behave differently depending on how
	// the store was built — the LSP violation that removed the option). The
	// ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := capabilitypg.NewUsageStore(q, h.PoolMigrate, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-charge")
	capID := uuid.New()
	seedCapRecord(t, h, capID, tenantID)

	// First charge: 4. Cap allows 10, tenant allows 5. Both fit.
	spent, err := store.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenantID, Amount: 4.0, MaxBudget: 10.0, UnitCode: "USD", Op: "", Actor: ""}, nil)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if spent.Spent < 3.99 || spent.Spent > 4.01 {
		t.Errorf("spent after first charge = %v, want ~4.00", spent.Spent)
	}

	// Configure a tenant cap of 5 (default = 0 = unlimited; we want
	// the rejection path).
	//
	// The charge above already created the accumulator row, so this is an
	// update and carries that row's version — the same read-then-write an
	// operator does from the console. Passing 0 here would (correctly) be
	// refused as "I believe no row exists".
	cur, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("read tenant budget: %v", err)
	}
	if _, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 5.0,
		ResetSpend:      false, // keep the 4 we already charged
		ExpectedVersion: cur.ResourceVersion,
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}

	// Second charge: 4. Cap accepts (8 ≤ 10). Tenant rejects (8 > 5).
	// Inner store should compensate the per-cap counter.
	_, err = store.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenantID, Amount: 4.0, MaxBudget: 10.0, UnitCode: "USD", Op: "", Actor: ""}, nil)
	if !errors.Is(err, capability.ErrTenantBudgetExceeded) {
		t.Fatalf("second charge: want ErrTenantBudgetExceeded, got %v", err)
	}

	// Verify the per-capability counter is back at 4 (not 8).
	usage, err := store.Get(ctx, capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.SpentAmount < 3.99 || usage.SpentAmount > 4.01 {
		t.Errorf("post-rejection cap spent = %v, want ~4.00 (compensation didn't fire)", usage.SpentAmount)
	}

	// Verify the tenant counter is at 4 too — the rejected charge
	// never went through.
	tb, err := store.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("get tenant budget: %v", err)
	}
	if tb.SpentAmount < 3.99 || tb.SpentAmount > 4.01 {
		t.Errorf("tenant spent = %v, want ~4.00", tb.SpentAmount)
	}
}

// TestCharge_RefundReturnsExactlyTheCharge: a refund larger than its charge
// is refused outright, and a full refund returns both counters to where they
// were — never below, which would grant the difference back as future budget.
func TestCharge_RefundReturnsExactlyTheCharge(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	q := sqlc.New(h.PoolMigrate)
	// Real pool: the store requires one (a nil pool used to mean "ledger
	// insert disabled", which made Charge behave differently depending on how
	// the store was built — the LSP violation that removed the option). The
	// ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := capabilitypg.NewUsageStore(q, h.PoolMigrate, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-refund")
	capID := uuid.New()
	seedCapRecord(t, h, capID, tenantID)

	receipt, err := store.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenantID, Amount: 1.0, MaxBudget: 0, UnitCode: "USD", Op: "", Actor: ""}, nil)
	if err != nil {
		t.Fatalf("seed charge: %v", err)
	}
	// Refund more than was charged — refused, and nothing moves.
	if _, err := store.Refund(ctx, capability.RefundRequest{ChargeID: receipt.ChargeID, Amount: 5.0}); !errors.Is(err, capability.ErrRefundExceedsCharge) {
		t.Fatalf("over-refund err = %v, want ErrRefundExceedsCharge", err)
	}
	// Refund the whole charge — both counters back to 0.
	if _, err := store.Refund(ctx, capability.RefundRequest{ChargeID: receipt.ChargeID}); err != nil {
		t.Fatalf("refund: %v", err)
	}

	u, err := store.Get(ctx, capID)
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
	q := sqlc.New(h.PoolMigrate)
	// Real pool: the store requires one (a nil pool used to mean "ledger
	// insert disabled", which made Charge behave differently depending on how
	// the store was built — the LSP violation that removed the option). The
	// ledger rows it writes are incidental here; this test is about the
	// running-total compensation path.
	store := capabilitypg.NewUsageStore(q, h.PoolMigrate, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-period")

	// Initial cap 10, charge 7 against an unrelated capability.
	initial, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 10.0,
		ResetSpend:      true,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	capID := uuid.New()
	seedCapRecord(t, h, capID, tenantID)
	if _, err := store.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenantID, Amount: 7.0, MaxBudget: 0, UnitCode: "USD", Op: "", Actor: ""}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	tb, _ := store.GetTenantBudget(ctx, tenantID)
	if tb.SpentAmount < 6.99 || tb.SpentAmount > 7.01 {
		t.Fatalf("pre-roll spent = %v, want 7", tb.SpentAmount)
	}

	// Roll the period: same cap (10), reset_spend=true. The roll carries the
	// version the create returned — Charge() does not bump it, so the caller's
	// read is still current.
	if _, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 10.0,
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
	q := sqlc.New(h.PoolMigrate)
	// Pool wired so the ledger insert fires.
	store := capabilitypg.NewUsageStore(q, h.PoolMigrate, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-ledger")
	capID := uuid.New()

	// Seed capability_records so the charges FK to it resolves.
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO capability_records
		 (id, tenant_id, issuer, principal_kind, principal_subject,
		  audience, caveats, created_by, expires_at)
		 VALUES ($1, $2, 'test-issuer', 'agent', 'agent-1',
		         '{admin}', '{}'::jsonb, 'test-issuer',
		         now() + interval '1 hour')`,
		capID, tenantID,
	); err != nil {
		t.Fatalf("seed capability_records: %v", err)
	}

	if _, err := store.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenantID, Amount: 2.5, MaxBudget: 10.0, UnitCode: "USD", Op: "presign.put", Actor: "agent-1"}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	var (
		count    int64
		amount   float64
		unitCode string
		op       string
		actor    string
	)
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount), 0)::float8,
		        COALESCE((SELECT unit_code FROM charges WHERE tenant_id = $1 LIMIT 1), ''),
		        COALESCE((SELECT op FROM charges WHERE tenant_id = $1 LIMIT 1), ''),
		        COALESCE((SELECT actor_subject FROM charges WHERE tenant_id = $1 LIMIT 1), '')
		 FROM charges WHERE tenant_id = $1`, tenantID,
	).Scan(&count, &amount, &unitCode, &op, &actor); err != nil {
		t.Fatalf("ledger probe: %v", err)
	}
	if count != 1 {
		t.Errorf("ledger rows = %d, want 1", count)
	}
	if amount < 2.49 || amount > 2.51 {
		t.Errorf("ledger amount = %v, want ~2.50", amount)
	}
	if unitCode != "USD" || op != "presign.put" || actor != "agent-1" {
		t.Errorf("ledger metadata = (%q, %q, %q), want (USD, presign.put, agent-1)",
			unitCode, op, actor)
	}
}
