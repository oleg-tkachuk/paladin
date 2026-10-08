//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
)

// copyRow reads one copy's counters as they are stored, past RLS.
func copyRow(t *testing.T, ctx context.Context, f lineageFixture, id string) (requests int64, spent, reserved capability.Nanos) {
	t.Helper()
	var s, r pgtype.Numeric
	err := f.pool.QueryRow(ctx,
		`SELECT request_count, spent, reserved FROM capability_copy_usage WHERE revocation_id = $1`,
		[]byte(id)).Scan(&requests, &s, &r)
	if err != nil {
		t.Fatalf("copy %s: %v", id, err)
	}
	return requests, nanosFrom(t, s), nanosFrom(t, r)
}

// ceiling is a copy's limits; budget is a decimal amount, "0" for none.
func ceiling(id string, requests int64, budget string) capability.CopyCeiling {
	return capability.CopyCeiling{
		RevocationID: []byte(id), MaxRequests: requests,
		MaxBudget: capability.MustParseAmount(budget),
	}
}

// Request limits per copy, on the runtime's NOBYPASSRLS connection: a copy
// stops at its own limit, a sibling counts apart, and the capability's limit
// still bounds them together.
func TestCopyRequestLimitsUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	bump := func(copies ...capability.CopyCeiling) error {
		_, err := f.usage.Bump(ledgerCtx, capability.BumpRequest{
			CapabilityID: f.root, TenantID: f.tenant, MaxRequests: 3, Copies: copies,
		})
		return err
	}
	one := ceiling("one", 1, "0")
	if err := bump(one); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := bump(one); !errors.Is(err, capability.ErrRequestLimitExceeded) {
		t.Fatalf("past the copy's limit: err = %v", err)
	}
	if n, _, _ := copyRow(t, ctx, f, "one"); n != 1 {
		t.Fatalf("a refused request left the copy at %d", n)
	}
	if u, err := f.usage.GetUsage(ledgerCtx, f.root); err != nil || u.RequestCount != 1 {
		t.Fatalf("a refused request moved the capability: %+v, %v", u, err)
	}
	sibling := ceiling("sibling", 5, "0")
	for range 2 {
		if err := bump(sibling); err != nil {
			t.Fatalf("sibling: %v", err)
		}
	}
	if err := bump(sibling); !errors.Is(err, capability.ErrRequestLimitExceeded) {
		t.Fatalf("the capability's limit did not bound its copies together: %v", err)
	}
}

// A copy's budget is held to on charge, returned on refund, and recorded on
// the ledger row so the refund finds it.
func TestCopyBudgetChargeAndRefund(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := ceiling("budgeted", 0, "2")
	charge := func(amount string) (capability.ChargeReceipt, error) {
		return f.usage.Charge(ledgerCtx, capability.ChargeRequest{
			CapabilityID: f.root, TenantID: f.tenant, Amount: capability.MustParseAmount(amount), MaxBudget: capability.MustParseAmount("25"), UnitCode: "USD",
			Copies: []capability.CopyCeiling{c},
		}, nil)
	}
	r, err := charge("1.5")
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := charge("1"); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("past the copy's budget: err = %v", err)
	}
	if f.spent(t, ledgerCtx, f.root) != capability.MustParseAmount("1.5") {
		t.Fatalf("a refused charge moved the capability")
	}
	var recorded [][]byte
	if err := f.pool.QueryRow(ctx, `SELECT copy_ids FROM charges WHERE id = $1`, r.ChargeID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || string(recorded[0]) != "budgeted" {
		t.Fatalf("ledger copy_ids = %q", recorded)
	}
	if _, err := f.usage.Refund(ledgerCtx, capability.RefundRequest{ChargeID: r.ChargeID}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if _, spent, _ := copyRow(t, ctx, f, "budgeted"); spent != 0 {
		t.Fatalf("copy spend after refund = %v", spent)
	}
	if _, err := charge("2"); err != nil {
		t.Fatalf("after refund: %v", err)
	}
}

// A hold counts against the copy's budget; settle checks and charges it,
// and the expiry sweep returns a lapsed hold to it.
func TestCopyBudgetReservations(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := ceiling("held", 0, "2")
	reserve := func(amount string, ttl time.Duration) (capability.Reservation, error) {
		return f.usage.Reserve(ledgerCtx, capability.ReserveRequest{
			CapabilityID: f.root, TenantID: f.tenant, Amount: capability.MustParseAmount(amount), MaxBudget: capability.MustParseAmount("25"), UnitCode: "USD",
			TTL: ttl, Copies: []capability.CopyCeiling{c},
		})
	}
	r, err := reserve("1.5", time.Hour)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := reserve("1", time.Hour); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("a hold past the copy's budget: err = %v", err)
	}
	if _, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: capability.MustParseAmount("2.5"), MaxBudget: capability.MustParseAmount("25")}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("settle past the copy's budget: err = %v", err)
	}
	if _, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: capability.MustParseAmount("1.75"), MaxBudget: capability.MustParseAmount("25")}, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, spent, reserved := copyRow(t, ctx, f, "held"); spent != capability.MustParseAmount("1.75") || reserved != 0 {
		t.Fatalf("copy after settle: spent %v, reserved %v", spent, reserved)
	}

	if _, err := reserve("0.25", time.Millisecond); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n, err := f.usage.ReleaseExpired(auth.WithCrossTenantRead(ctx)); err != nil || n != 1 {
		t.Fatalf("ReleaseExpired = %d, %v", n, err)
	}
	if _, _, reserved := copyRow(t, ctx, f, "held"); reserved != 0 {
		t.Fatalf("copy still holds %v after the sweep", reserved)
	}
}

// The copy table's own policy: another tenant can neither write a counter
// for this tenant's capability nor read one.
func TestCopyUsageIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	pool := rlsPool(t, ctx, f.pool)
	other, _ := mkTenant(t, ctx, f.pool, "shared")
	otherCtx := auth.WithActingTenant(ctx, other)

	if _, err := pool.Exec(otherCtx,
		`INSERT INTO capability_copy_usage (revocation_id, capability_id) VALUES ($1, $2)`,
		[]byte("foreign"), f.root); err == nil {
		t.Fatal("another tenant wrote a copy counter for this tenant's capability")
	}

	ownCtx := auth.WithActingTenant(ctx, f.tenant)
	if _, err := pool.Exec(ownCtx,
		`INSERT INTO capability_copy_usage (revocation_id, capability_id) VALUES ($1, $2)`,
		[]byte("own"), f.root); err != nil {
		t.Fatalf("the owning tenant: %v", err)
	}
	var seen int
	if err := pool.QueryRow(otherCtx, `SELECT count(*) FROM capability_copy_usage`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Fatalf("another tenant reads %d copy counters", seen)
	}
}

// CopyUsage reads back the counters under the owning tenant, and another
// tenant reads nothing.
func TestCopyUsageIsReadBackPerTenant(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := ceiling("read", 5, "2")
	if _, err := f.usage.Bump(ledgerCtx, capability.BumpRequest{
		CapabilityID: f.root, TenantID: f.tenant, Copies: []capability.CopyCeiling{c},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.usage.Charge(ledgerCtx, capability.ChargeRequest{
		CapabilityID: f.root, TenantID: f.tenant, Amount: capability.MustParseAmount("0.5"), MaxBudget: capability.MustParseAmount("25"), UnitCode: "USD",
		Copies: []capability.CopyCeiling{c},
	}, nil); err != nil {
		t.Fatal(err)
	}
	ids := [][]byte{[]byte("read"), []byte("never")}
	got, err := f.usage.CopyUsage(ledgerCtx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].RevocationID) != "read" || got[0].CapabilityID != f.root ||
		got[0].RequestCount != 1 || got[0].SpentAmount != capability.MustParseAmount("0.5") || got[0].ReservedAmount != 0 {
		t.Fatalf("CopyUsage = %+v", got)
	}
	other, _ := mkTenant(t, ctx, f.pool, "shared")
	if got, err := f.usage.CopyUsage(auth.WithActingTenant(ctx, other), ids); err != nil || len(got) != 0 {
		t.Fatalf("another tenant read %+v, %v", got, err)
	}
}
