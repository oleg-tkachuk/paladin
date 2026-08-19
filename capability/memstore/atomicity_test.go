package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/capability"
)

// The charge-atomicity guard (FR-011 / SC-008).
//
// This property is why UsageStore is generic in TX at all: onCharged runs
// inside the charge so a consumer's outbox write commits with the spend,
// closing the dual-write window (ADR-0003). Before this file, it had NO test
// anywhere in the repository — verified by grepping every *_test.go for
// onCharged and finding nothing. The guard therefore closes a pre-existing
// gap rather than merely preserving coverage across the move.

func TestChargeRollsBackWhenSideEffectFails(t *testing.T) {
	ctx := context.Background()
	capID, tenantID := uuid.New(), uuid.New()

	usage := NewUsage[struct{}](nil)
	if _, err := usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: tenantID, MaxBudgetAmount: 100, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("SetTenantBudget: %v", err)
	}

	// A first charge that succeeds, so there is real state to preserve.
	if _, err := usage.Charge(ctx, capID, 10, 100, "USD", tenantID, "read", "alice", nil); err != nil {
		t.Fatalf("first charge: %v", err)
	}

	before, err := usage.Get(ctx, capID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	budgetBefore, err := usage.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenantBudget: %v", err)
	}
	ledgerBefore := len(usage.Ledger())

	// Now a charge whose side effect fails.
	boom := errors.New("outbox insert failed")
	spent, err := usage.Charge(ctx, capID, 25, 100, "USD", tenantID, "write", "alice",
		func(context.Context, struct{}) error { return boom })

	if !errors.Is(err, boom) {
		t.Fatalf("want the side-effect error, got %v", err)
	}

	// The whole point: nothing moved.
	after, err := usage.Get(ctx, capID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.SpentAmount != before.SpentAmount {
		t.Errorf("capability spend moved: %v → %v", before.SpentAmount, after.SpentAmount)
	}
	if spent != before.SpentAmount {
		t.Errorf("returned spend = %v, want the unchanged %v", spent, before.SpentAmount)
	}

	budgetAfter, err := usage.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenantBudget: %v", err)
	}
	if budgetAfter.SpentAmount != budgetBefore.SpentAmount {
		t.Errorf("tenant spend moved: %v → %v", budgetBefore.SpentAmount, budgetAfter.SpentAmount)
	}

	// The ledger is the third thing that must not have grown — a committed
	// ledger row for a rolled-back charge is exactly the dual-write divergence
	// this design exists to prevent.
	if got := len(usage.Ledger()); got != ledgerBefore {
		t.Errorf("ledger grew from %d to %d despite the rollback", ledgerBefore, got)
	}
}

// A retry after a failed side effect must land cleanly — if the first attempt
// had partially applied, the retry would double-charge.
func TestChargeRetryAfterRollbackIsClean(t *testing.T) {
	ctx := context.Background()
	capID, tenantID := uuid.New(), uuid.New()
	usage := NewUsage[struct{}](nil)

	boom := errors.New("transient")
	if _, err := usage.Charge(ctx, capID, 7, 100, "USD", tenantID, "op", "actor",
		func(context.Context, struct{}) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("want the failure, got %v", err)
	}

	spent, err := usage.Charge(ctx, capID, 7, 100, "USD", tenantID, "op", "actor", nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if spent != 7 {
		t.Errorf("spend after one failed + one successful charge = %v, want 7", spent)
	}
	if got := len(usage.Ledger()); got != 1 {
		t.Errorf("ledger has %d entries, want exactly 1", got)
	}
}

// Rejection by the capability ceiling must leave both counters untouched, so a
// caller can retry with a smaller amount without compensating anything.
func TestChargeCapabilityCeilingLeavesBothCountersUnmutated(t *testing.T) {
	ctx := context.Background()
	capID, tenantID := uuid.New(), uuid.New()
	usage := NewUsage[struct{}](nil)
	if _, err := usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: tenantID, MaxBudgetAmount: 1000, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("SetTenantBudget: %v", err)
	}

	_, err := usage.Charge(ctx, capID, 50, 10, "USD", tenantID, "op", "actor", nil)
	if !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}

	if _, err := usage.Get(ctx, capID); !errors.Is(err, capability.ErrUsageNotFound) {
		t.Errorf("a rejected first charge must create no usage row, got %v", err)
	}
	b, err := usage.GetTenantBudget(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenantBudget: %v", err)
	}
	if b.SpentAmount != 0 {
		t.Errorf("tenant spend = %v after a capability-ceiling rejection, want 0", b.SpentAmount)
	}
}

// The tenant ceiling is checked AFTER the capability one, so this is the case
// where a naive implementation would have already applied the capability side
// and would need a compensating refund. Staging removes that need entirely.
func TestChargeTenantCeilingLeavesBothCountersUnmutated(t *testing.T) {
	ctx := context.Background()
	capID, tenantID := uuid.New(), uuid.New()
	usage := NewUsage[struct{}](nil)
	if _, err := usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: tenantID, MaxBudgetAmount: 20, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("SetTenantBudget: %v", err)
	}

	// Comfortably inside the capability ceiling, over the tenant one.
	_, err := usage.Charge(ctx, capID, 50, 1000, "USD", tenantID, "op", "actor", nil)
	if !errors.Is(err, capability.ErrTenantBudgetExceeded) {
		t.Fatalf("want ErrTenantBudgetExceeded, got %v", err)
	}

	if _, err := usage.Get(ctx, capID); !errors.Is(err, capability.ErrUsageNotFound) {
		t.Errorf("capability counter was mutated before the tenant check rejected: %v", err)
	}
	b, _ := usage.GetTenantBudget(ctx, tenantID)
	if b.SpentAmount != 0 {
		t.Errorf("tenant spend = %v, want 0", b.SpentAmount)
	}
	if got := len(usage.Ledger()); got != 0 {
		t.Errorf("ledger has %d entries after a rejection, want 0", got)
	}
}

// The no-transaction mode (contracts §1.2): a consumer with no transactional
// storage instantiates UsageStore[struct{}] and always passes nil. Everything
// works; only the atomic-side-effect guarantee is forgone.
func TestNoTransactionModeWorksEndToEnd(t *testing.T) {
	ctx := context.Background()
	capID := uuid.New()
	usage := NewUsage[struct{}](nil)

	if _, err := usage.BumpRequest(ctx, capID, 5); err != nil {
		t.Fatalf("BumpRequest: %v", err)
	}
	spent, err := usage.Charge(ctx, capID, 3, 10, "", uuid.Nil, "op", "actor", nil)
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if spent != 3 {
		t.Errorf("spent = %v, want 3", spent)
	}
	u, err := usage.Get(ctx, capID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", u.RequestCount)
	}
	// An empty unit code must resolve to the default rather than persisting
	// blank — a bare number with no unit is unrenderable.
	if u.UnitCode != capability.DefaultUnitCode {
		t.Errorf("UnitCode = %q, want the %q default", u.UnitCode, capability.DefaultUnitCode)
	}
}
