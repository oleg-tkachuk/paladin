//go:build integration

package components

import (
	"errors"
	"sync"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
)

// concurrentDeliveries is how many times the same cost arrives at once.
const concurrentDeliveries = 20

// A cost delivered many times at once is charged once: the advisory lock
// serialises the deliveries, and every one after the first is a replay.
func TestChargeByExternalRefIsChargedOnce(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		fresh    int
		chargeID = map[string]bool{}
	)
	for range concurrentDeliveries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.usage.Charge(ledgerCtx, capability.ChargeRequest{
				CapabilityID: f.child, TenantID: f.tenant, Amount: 2, MaxBudget: 20, UnitCode: "USD",
				ExternalRef: "call-1",
			}, nil)
			if err != nil {
				t.Errorf("charge: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			chargeID[r.ChargeID.String()] = true
			if !r.Replayed {
				fresh++
			}
		}()
	}
	wg.Wait()
	if fresh != 1 || len(chargeID) != 1 {
		t.Fatalf("%d fresh charges over %d charge ids, want one of each", fresh, len(chargeID))
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM charges WHERE capability_id = $1`, f.child).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("ledger holds %d rows for the child, want 1", rows)
	}
	if got := f.spent(t, ledgerCtx, f.root); !closeEnough(got, 2) {
		t.Errorf("parent spent %v, want 2", got)
	}
}

// A cost already incurred is recorded past the capability's, its parent's
// and the tenant's ceilings, and the crossed ceiling refuses what follows.
func TestOverrunRecordChargesPastEveryCeiling(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	if _, err := f.usage.SetTenantBudget(ledgerCtx, capability.SetTenantBudgetArgs{
		TenantID: f.tenant, MaxBudgetAmount: 22, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}
	charge := func(amount float64, p capability.OverrunPolicy) (capability.ChargeReceipt, error) {
		return f.usage.Charge(ledgerCtx, capability.ChargeRequest{
			CapabilityID: f.child, TenantID: f.tenant, Amount: amount, MaxBudget: 20, UnitCode: "USD", Overrun: p,
		}, nil)
	}

	// 30 crosses the child's 20, the parent's 25 and the tenant's 22.
	if _, err := charge(30, capability.OverrunReject); err == nil {
		t.Fatal("a rejecting charge past every ceiling was accepted")
	}
	r, err := charge(30, capability.OverrunRecord)
	if err != nil || !r.Overrun || !closeEnough(r.Spent, 30) {
		t.Fatalf("recording charge = %+v, %v; want 30 spent, overrun", r, err)
	}
	if got := f.spent(t, ledgerCtx, f.root); !closeEnough(got, 30) {
		t.Errorf("parent spent %v, want 30", got)
	}
	if b, err := f.usage.GetTenantBudget(ledgerCtx, f.tenant); err != nil || !closeEnough(b.SpentAmount, 30) {
		t.Errorf("tenant = %+v, %v; want 30 spent", b, err)
	}
	var overrun bool
	if err := f.pool.QueryRow(ctx, `SELECT overrun FROM charges WHERE id = $1`, r.ChargeID).Scan(&overrun); err != nil || !overrun {
		t.Errorf("ledger row overrun = %v, %v; want true", overrun, err)
	}
	if _, err := charge(0.01, capability.OverrunReject); err == nil {
		t.Error("after an overrun a rejecting charge was accepted")
	}
}

func TestSettleRecordsAnOverrunAndReplays(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	res, err := f.usage.Reserve(ledgerCtx, capability.ReserveRequest{
		CapabilityID: f.child, TenantID: f.tenant, Amount: 10, MaxBudget: 20, UnitCode: "USD",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	r, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{
		ReservationID: res.ID, Amount: 24, MaxBudget: 20, Overrun: capability.OverrunRecord,
	}, nil)
	if err != nil || !r.Overrun || !closeEnough(r.Spent, 24) {
		t.Fatalf("recording settle = %+v, %v; want 24 spent, overrun", r, err)
	}
	if u, _ := f.usage.Get(ledgerCtx, f.root); u.ReservedAmount != 0 {
		t.Errorf("parent still holds %v after settling", u.ReservedAmount)
	}
	again, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: res.ID, Amount: 1, MaxBudget: 20}, nil)
	if err != nil || !again.Replayed || !again.Overrun || again.ChargeID != r.ChargeID {
		t.Errorf("replayed settle = %+v, %v; want the overrun charge again", again, err)
	}
}

// A Biscuit copy's own budget is crossed and counted under OverrunRecord.
func TestOverrunRecordCrossesACopyBudget(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	copyID := []byte("copy-1")
	copies := []capability.CopyCeiling{{RevocationID: copyID, MaxBudgetMicros: capability.MicrosPerUnit}}
	charge := func(p capability.OverrunPolicy) (capability.ChargeReceipt, error) {
		return f.usage.Charge(ledgerCtx, capability.ChargeRequest{
			CapabilityID: f.child, TenantID: f.tenant, Amount: 2, MaxBudget: 20, UnitCode: "USD", Copies: copies, Overrun: p,
		}, nil)
	}
	if _, err := charge(capability.OverrunReject); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("rejecting charge past the copy's budget: err = %v, want ErrBudgetExceeded", err)
	}
	r, err := charge(capability.OverrunRecord)
	if err != nil || !r.Overrun {
		t.Fatalf("recording charge = %+v, %v; want an overrun", r, err)
	}
	got, err := f.usage.CopyUsage(ledgerCtx, [][]byte{copyID})
	if err != nil || len(got) != 1 || !closeEnough(got[0].SpentAmount, 2) {
		t.Errorf("copy usage = %+v, %v; want 2 spent", got, err)
	}
}
