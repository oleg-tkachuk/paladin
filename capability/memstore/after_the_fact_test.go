package memstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// A cost reported more than once — an event delivered at least once — is
// charged once, and the repeats return the first charge.
func TestChargeIsIdempotentOnExternalRef(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, _ := reservationFixture(t)
	fanOuts := 0
	onCharged := func(context.Context, struct{}) error { fanOuts++; return nil }
	charge := func(capID uuid.UUID, ref string, amount float64) (capability.ChargeReceipt, error) {
		return u.Charge(ctx, capability.ChargeRequest{
			CapabilityID: capID, TenantID: tenant, Amount: amount, MaxBudget: 10, ExternalRef: ref,
		}, onCharged)
	}

	first, err := charge(capID, "call-1", 2)
	if err != nil || first.Replayed {
		t.Fatalf("first charge = %+v, %v", first, err)
	}
	again, err := charge(capID, "call-1", 5)
	if err != nil || !again.Replayed || again.ChargeID != first.ChargeID || again.Spent != 2 {
		t.Fatalf("repeated charge = %+v, %v; want a replay of %s at spend 2", again, err, first.ChargeID)
	}
	if fanOuts != 1 {
		t.Errorf("onCharged ran %d times, want once", fanOuts)
	}

	// The key is per capability, and an empty one never deduplicates.
	if r, err := charge(uuid.New(), "call-1", 1); err != nil || r.Replayed {
		t.Errorf("same ref on another capability = %+v, %v; want a fresh charge", r, err)
	}
	for range 2 {
		if r, err := charge(capID, "", 1); err != nil || r.Replayed {
			t.Errorf("charge without a ref = %+v, %v; want a fresh charge", r, err)
		}
	}
	if got, _ := u.Get(ctx, capID); got.SpentAmount != 4 {
		t.Errorf("spent = %v, want 2 + 1 + 1", got.SpentAmount)
	}
}

func TestChargeRefusesAMalformedRequest(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, _ := reservationFixture(t)
	cases := map[string]capability.ChargeRequest{
		"ref too long":      {ExternalRef: strings.Repeat("x", capability.MaxExternalRefBytes+1)},
		"ref not printable": {ExternalRef: "call\n1"},
		"unknown overrun":   {Overrun: capability.OverrunRecord + 1},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			req.CapabilityID, req.TenantID, req.Amount = capID, tenant, 1
			if _, err := u.Charge(ctx, req, nil); !errors.Is(err, capability.ErrInvalidRequest) {
				t.Fatalf("Charge = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if _, err := u.Settle(ctx, capability.SettleRequest{ReservationID: uuid.New(), Amount: 1, Overrun: capability.OverrunRecord + 1}, nil); !errors.Is(err, capability.ErrInvalidRequest) {
		t.Fatalf("Settle with an unknown overrun = %v, want ErrInvalidRequest", err)
	}
	if len(u.Ledger()) != 0 {
		t.Errorf("a refused request wrote the ledger: %+v", u.Ledger())
	}
}

// A cost already incurred is recorded past every ceiling it crosses, and the
// crossed ceiling then refuses what comes next.
func TestOverrunRecordChargesPastEveryCeiling(t *testing.T) {
	ctx := context.Background()
	records := New[struct{}]()
	u := NewUsage(records)
	tenant := uuid.New()
	if _, err := u.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{TenantID: tenant, MaxBudgetAmount: 5}); err != nil {
		t.Fatal(err)
	}
	parent := capability.Capability{ID: uuid.New(), Subject: capability.Principal{TenantID: tenant},
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}, MaxBudgetAmount: 4}}
	child := capability.Capability{ID: uuid.New(), ParentID: parent.ID, Subject: capability.Principal{TenantID: tenant},
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}, MaxBudgetAmount: 3}}
	for _, c := range []capability.Capability{parent, child} {
		if err := records.Insert(ctx, c, capability.Principal{Subject: "op"}); err != nil {
			t.Fatal(err)
		}
	}
	charge := func(amount float64, p capability.OverrunPolicy) (capability.ChargeReceipt, error) {
		return u.Charge(ctx, capability.ChargeRequest{
			CapabilityID: child.ID, TenantID: tenant, Amount: amount, MaxBudget: 3, Overrun: p,
		}, nil)
	}

	if _, err := charge(6, capability.OverrunReject); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("rejecting policy: err = %v, want ErrBudgetExceeded", err)
	}
	r, err := charge(6, capability.OverrunRecord)
	if err != nil || !r.Overrun || r.Spent != 6 {
		t.Fatalf("recording policy = %+v, %v; want 6 spent past the ceiling", r, err)
	}
	for id, want := range map[uuid.UUID]float64{child.ID: 6, parent.ID: 6} {
		if got, _ := u.Get(ctx, id); got.SpentAmount != want {
			t.Errorf("spent on %s = %v, want %v", id, got.SpentAmount, want)
		}
	}
	if b, _ := u.GetTenantBudget(ctx, tenant); b.SpentAmount != 6 {
		t.Errorf("tenant spent = %v, want 6", b.SpentAmount)
	}
	if l := u.Ledger(); len(l) != 1 || !l[0].Overrun {
		t.Errorf("ledger = %+v, want one charge marked as an overrun", l)
	}
	if r, err := charge(1, capability.OverrunRecord); err != nil || !r.Overrun {
		t.Errorf("a second recorded charge = %+v, %v", r, err)
	}
	if _, err := charge(0.01, capability.OverrunReject); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Errorf("after an overrun a rejecting charge = %v, want ErrBudgetExceeded", err)
	}
	if _, err := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: child.ID, TenantID: tenant, Amount: 0.01, MaxBudget: 3}); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Errorf("after an overrun a reservation = %v, want ErrBudgetExceeded", err)
	}
}

func TestSettleRecordsAnOverrunOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, _ := reservationFixture(t)
	r, err := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: 8, MaxBudget: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 12, MaxBudget: 10}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("rejecting settle: err = %v, want ErrBudgetExceeded", err)
	}
	receipt, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 12, MaxBudget: 10, Overrun: capability.OverrunRecord}, nil)
	if err != nil || !receipt.Overrun || receipt.Spent != 12 {
		t.Fatalf("recording settle = %+v, %v; want 12 spent, overrun", receipt, err)
	}
	if got, _ := u.Get(ctx, capID); got.ReservedAmount != 0 {
		t.Errorf("hold left after settling = %v", got.ReservedAmount)
	}
	again, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 1, MaxBudget: 10}, nil)
	if err != nil || !again.Replayed || !again.Overrun || again.ChargeID != receipt.ChargeID {
		t.Errorf("replayed settle = %+v, %v; want the overrun charge again", again, err)
	}
}
