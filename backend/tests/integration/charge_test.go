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

	"github.com/oleg-tkachuk/paladin-private/capability"
	capabilitypg "github.com/oleg-tkachuk/paladin-private/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin-private/tests/integration/pgharness"
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
	// nil pool → ledger insert disabled; this test exercises the
	// running-total compensation path, not the ledger surface.
	store := capabilitypg.NewUsageStore(q, nil, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-charge")
	capID := uuid.New()

	// First charge: 4. Cap allows 10, tenant allows 5. Both fit.
	spent, err := store.Charge(ctx, capID, 4.0, 10.0, "USD", tenantID, "", "", nil)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if spent < 3.99 || spent > 4.01 {
		t.Errorf("spent after first charge = %v, want ~4.00", spent)
	}

	// Configure a tenant cap of 5 (default = 0 = unlimited; we want
	// the rejection path).
	if _, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 5.0,
		ResetSpend:      false, // keep the 4 we already charged
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}

	// Second charge: 4. Cap accepts (8 ≤ 10). Tenant rejects (8 > 5).
	// Inner store should compensate the per-cap counter.
	_, err = store.Charge(ctx, capID, 4.0, 10.0, "USD", tenantID, "", "", nil)
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

// TestCharge_RefundFloorsAtZero: refund larger than current spend
// must floor at 0, not go negative (which would silently grant the
// difference back as future budget).
func TestCharge_RefundFloorsAtZero(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	q := sqlc.New(h.PoolMigrate)
	// nil pool → ledger insert disabled; this test exercises the
	// running-total compensation path, not the ledger surface.
	store := capabilitypg.NewUsageStore(q, nil, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-refund")
	capID := uuid.New()

	if _, err := store.Charge(ctx, capID, 1.0, 0, "USD", tenantID, "", "", nil); err != nil {
		t.Fatalf("seed charge: %v", err)
	}
	// Refund 5 — flooring at 0 means the row reads 0 after.
	if err := store.RefundCapability(ctx, capID, 5.0); err != nil {
		t.Fatalf("refund cap: %v", err)
	}
	if err := store.RefundTenant(ctx, tenantID, 5.0); err != nil {
		t.Fatalf("refund tenant: %v", err)
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
	// nil pool → ledger insert disabled; this test exercises the
	// running-total compensation path, not the ledger surface.
	store := capabilitypg.NewUsageStore(q, nil, nil)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-period")

	// Initial cap 10, charge 7 against an unrelated capability.
	if _, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 10.0,
		ResetSpend:      true,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	capID := uuid.New()
	if _, err := store.Charge(ctx, capID, 7.0, 0, "USD", tenantID, "", "", nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	tb, _ := store.GetTenantBudget(ctx, tenantID)
	if tb.SpentAmount < 6.99 || tb.SpentAmount > 7.01 {
		t.Fatalf("pre-roll spent = %v, want 7", tb.SpentAmount)
	}

	// Roll the period: same cap (10), reset_spend=true.
	if _, err := store.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: 10.0,
		ResetSpend:      true,
	}); err != nil {
		t.Fatalf("roll: %v", err)
	}
	tb, _ = store.GetTenantBudget(ctx, tenantID)
	if tb.SpentAmount != 0 {
		t.Errorf("post-roll spent = %v, want 0", tb.SpentAmount)
	}
}

// TestCharge_LedgerRowAppearsAfterCharge: a successful Charge() must
// also write a row into the charges ledger (migration 027) carrying
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
		  audience, caveats, expires_at)
		 VALUES ($1, $2, 'test-issuer', 'agent', 'agent-1',
		         '{admin}', '{}'::jsonb, now() + interval '1 hour')`,
		capID, tenantID,
	); err != nil {
		t.Fatalf("seed capability_records: %v", err)
	}

	if _, err := store.Charge(ctx, capID, 2.5, 10.0, "USD", tenantID, "presign.put", "agent-1", nil); err != nil {
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
