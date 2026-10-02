package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

func reservationFixture(t *testing.T) (*UsageStore[struct{}], uuid.UUID, uuid.UUID, *time.Time) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	u := NewUsage[struct{}](nil).WithClock(func() time.Time { return now })
	tenant := uuid.New()
	if _, err := u.SetTenantBudget(context.Background(), capability.SetTenantBudgetArgs{TenantID: tenant, MaxBudgetAmount: 100}); err != nil {
		t.Fatal(err)
	}
	return u, uuid.New(), tenant, &now
}

// A hold counts against the ceiling like spend does, so two callers cannot
// each reserve the same last 6.00.
func TestReservationsCountAgainstTheCeiling(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, _ := reservationFixture(t)
	reserve := func(amount float64) (capability.Reservation, error) {
		return u.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: amount, MaxBudget: 10})
	}
	if _, err := reserve(6); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	if _, err := reserve(6); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("second hold past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if _, err := u.Charge(ctx, capability.ChargeRequest{CapabilityID: capID, TenantID: tenant, Amount: 5, MaxBudget: 10}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("charge past spend+holds: err = %v, want ErrBudgetExceeded", err)
	}
	got, _ := u.Get(ctx, capID)
	if got.ReservedAmount != 6 || got.SpentAmount != 0 {
		t.Errorf("usage = %+v, want 6 held and nothing spent", got)
	}
}

func TestSettleChargesTheActualCostAndReleasesTheHold(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, _ := reservationFixture(t)
	r, err := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: 8, MaxBudget: 10, Op: "llm", Actor: "agent"})
	if err != nil {
		t.Fatal(err)
	}

	// Above the hold, the excess must fit: 8 held, 11 actual, ceiling 10.
	if _, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 11, MaxBudget: 10}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("settle past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if got, _ := u.Get(ctx, capID); got.ReservedAmount != 8 {
		t.Fatalf("a refused settle released the hold: %+v", got)
	}

	receipt, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 3, MaxBudget: 10}, nil)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	got, _ := u.Get(ctx, capID)
	if got.SpentAmount != 3 || got.ReservedAmount != 0 || receipt.ChargeID == uuid.Nil {
		t.Errorf("after settle usage = %+v receipt = %+v, want 3 spent, 0 held, a charge", got, receipt)
	}
	if b, _ := u.GetTenantBudget(ctx, tenant); b.SpentAmount != 3 || b.ReservedAmount != 0 {
		t.Errorf("tenant after settle = %+v", b)
	}
	if l := u.Ledger(); len(l) != 1 || l[0].Op != "llm" || l[0].Actor != "agent" {
		t.Errorf("ledger = %+v, want one charge carrying the reservation's op and actor", l)
	}
	if _, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 1, MaxBudget: 10}, nil); !errors.Is(err, capability.ErrReservationNotFound) {
		t.Errorf("second settle: err = %v, want ErrReservationNotFound", err)
	}
}

func TestReleaseAndExpiryFreeTheHold(t *testing.T) {
	ctx := context.Background()
	u, capID, tenant, now := reservationFixture(t)
	r, _ := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: 4, MaxBudget: 10})
	if err := u.Release(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := u.Release(ctx, r.ID); err != nil {
		t.Fatalf("repeated release: %v", err)
	}
	if got, _ := u.Get(ctx, capID); got.ReservedAmount != 0 {
		t.Fatalf("held after release = %v", got.ReservedAmount)
	}

	r, _ = u.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: 4, MaxBudget: 10, TTL: time.Minute})
	*now = now.Add(2 * time.Minute)
	if _, err := u.Settle(ctx, capability.SettleRequest{ReservationID: r.ID, Amount: 1, MaxBudget: 10}, nil); !errors.Is(err, capability.ErrReservationNotFound) {
		t.Fatalf("settle after expiry: err = %v, want ErrReservationNotFound", err)
	}
	if n, _ := u.ReleaseExpired(ctx); n != 1 {
		t.Fatalf("ReleaseExpired released %d, want 1", n)
	}
	if got, _ := u.Get(ctx, capID); got.ReservedAmount != 0 {
		t.Fatalf("held after expiry sweep = %v", got.ReservedAmount)
	}
}

// A child's hold counts against its parent, like its spend does.
func TestReservationsCountAgainstAncestors(t *testing.T) {
	ctx := context.Background()
	s := New[struct{}]()
	u := NewUsage(s)
	rootID, kids, tenant := tree(t, s,
		capability.Caveats{Ops: []capability.Op{capability.OpGet}, MaxBudgetAmount: 25, UnitCode: "USD"},
		2,
		capability.Caveats{Ops: []capability.Op{capability.OpGet}, MaxBudgetAmount: 20, UnitCode: "USD"},
	)
	if _, err := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: kids[0], TenantID: tenant, Amount: 20, MaxBudget: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Reserve(ctx, capability.ReserveRequest{CapabilityID: kids[1], TenantID: tenant, Amount: 20, MaxBudget: 20}); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("sibling hold past the parent: err = %v, want ErrBudgetExceeded", err)
	}
	if got, _ := u.Get(ctx, rootID); got.ReservedAmount != 20 {
		t.Errorf("parent held = %v, want 20", got.ReservedAmount)
	}
}
