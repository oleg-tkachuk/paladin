package auth

import (
	"context"
	"errors"
	"net"
	"testing"

	"connectrpc.com/connect"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/capability"
)

// fakeUsage is an in-memory UsageStore for the caveat tests. Keeps a
// per-cap counter and spend; emulates the SQL UPSERT-and-check
// semantics by computing post-increment, comparing to the cap, and
// returning the right sentinel.
type fakeUsage struct {
	requests map[uuid.UUID]int64
	spent    map[uuid.UUID]float64
}

func newFakeUsage() *fakeUsage {
	return &fakeUsage{
		requests: map[uuid.UUID]int64{},
		spent:    map[uuid.UUID]float64{},
	}
}

func (f *fakeUsage) BumpRequest(_ context.Context, id uuid.UUID, max int64) (int64, error) {
	next := f.requests[id] + 1
	if max > 0 && next > max {
		return 0, capability.ErrRequestLimitExceeded
	}
	f.requests[id] = next
	return next, nil
}

func (f *fakeUsage) Charge(_ context.Context, id uuid.UUID, amount, max float64, _ string, _ uuid.UUID, _ string, _ string) (float64, error) {
	next := f.spent[id] + amount
	if max > 0 && next > max {
		return 0, capability.ErrBudgetExceeded
	}
	f.spent[id] = next
	return next, nil
}

func (f *fakeUsage) RefundCapability(_ context.Context, id uuid.UUID, amount float64) error {
	v := f.spent[id] - amount
	if v < 0 {
		v = 0
	}
	f.spent[id] = v
	return nil
}

func (f *fakeUsage) RefundTenant(_ context.Context, _ uuid.UUID, _ float64) error { return nil }

func (f *fakeUsage) Get(_ context.Context, id uuid.UUID) (capability.Usage, error) {
	c, ok := f.requests[id]
	s, sok := f.spent[id]
	if !ok && !sok {
		return capability.Usage{}, capability.ErrUsageNotFound
	}
	return capability.Usage{CapabilityID: id, RequestCount: c, SpentAmount: s, UnitCode: capability.DefaultUnitCode}, nil
}

func (f *fakeUsage) GetTenantBudget(_ context.Context, _ uuid.UUID) (capability.TenantBudget, error) {
	return capability.TenantBudget{}, capability.ErrTenantBudgetNotFound
}

func (f *fakeUsage) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetArgs) (capability.TenantBudget, error) {
	return capability.TenantBudget{TenantID: args.TenantID, MaxBudgetAmount: args.MaxBudgetAmount, UnitCode: args.UnitCode}, nil
}

func (f *fakeUsage) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.requests, id)
	delete(f.spent, id)
	return nil
}

func (f *fakeUsage) PurgeOrphans(_ context.Context) (int64, error) { return 0, nil }

// ─── ipInAnyCIDR ────────────────────────────────────────────────────

func TestIpInAnyCIDR(t *testing.T) {
	cases := []struct {
		ip    string
		cidrs []string
		want  bool
	}{
		{"10.0.0.5", []string{"10.0.0.0/8"}, true},
		{"192.168.1.5", []string{"10.0.0.0/8"}, false},
		{"192.168.1.5", []string{"10.0.0.0/8", "192.168.0.0/16"}, true},
		{"10.0.0.5", []string{"not-a-cidr", "10.0.0.0/8"}, true}, // bad cidr skipped
		{"10.0.0.5", []string{}, false},                          // empty list = no match
	}
	for _, c := range cases {
		got := ipInAnyCIDR(net.ParseIP(c.ip), c.cidrs)
		if got != c.want {
			t.Errorf("ip=%q cidrs=%v got=%v want=%v", c.ip, c.cidrs, got, c.want)
		}
	}
}

// ─── ChargeCapability ───────────────────────────────────────────────

func TestChargeCapability_NoCapability_NoOp(t *testing.T) {
	if err := ChargeCapability(context.Background(), 1.0, ""); err != nil {
		t.Errorf("no capability + Charge must be no-op, got %v", err)
	}
}

func TestChargeCapability_NoStoreInContext_NoOp(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 1.0}}
	ctx := WithCapability(context.Background(), cap)
	if err := ChargeCapability(ctx, 0.5, ""); err != nil {
		t.Errorf("no store on context = no-op, got %v", err)
	}
}

func TestChargeCapability_RecordsAndAllows(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.30, ""); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if err := ChargeCapability(ctx, 0.30, ""); err != nil {
		t.Fatalf("second charge: %v", err)
	}
	u, _ := store.Get(ctx, cap.ID)
	if u.SpentAmount < 0.59 || u.SpentAmount > 0.61 { // float wiggle
		t.Errorf("spent = %v, want ~0.60", u.SpentAmount)
	}
}

func TestChargeCapability_OverBudget_Rejects(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.80, ""); err != nil {
		t.Fatalf("0.80 charge: %v", err)
	}
	err := ChargeCapability(ctx, 0.50, "") // would exceed 1.00
	if err == nil {
		t.Fatal("expected over-budget error")
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeResourceExhausted {
		t.Errorf("expected CodeResourceExhausted, got %v", err)
	}
}

func TestChargeCapability_UnlimitedBudget_NeverRejects(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	for i := 0; i < 100; i++ {
		if err := ChargeCapability(ctx, 100.0, ""); err != nil {
			t.Fatalf("unlimited budget should never reject, got %v at i=%d", err, i)
		}
	}
}

func TestChargeCapability_NegativeOrZero_NoOp(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0, ""); err != nil {
		t.Errorf("zero charge must be no-op, got %v", err)
	}
	if err := ChargeCapability(ctx, -1, ""); err != nil {
		t.Errorf("negative charge must be no-op, got %v", err)
	}
	if u, _ := store.Get(ctx, cap.ID); u.SpentAmount != 0 {
		t.Errorf("spent should be 0, got %v", u.SpentAmount)
	}
}

// ─── ChargeRequest ──────────────────────────────────────────────────

func TestChargeRequest_NoAmountInContext_NoOp(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New()}
	// Cap + store but NO amount on context — handler shouldn't be
	// charged because cfg.ChargePerRequest defaulted to 0.
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeRequest(ctx); err != nil {
		t.Errorf("no amount stamped = no-op, got %v", err)
	}
	if u, _ := store.Get(ctx, cap.ID); u.SpentAmount != 0 {
		t.Errorf("spent should stay 0, got %v", u.SpentAmount)
	}
}

func TestChargeRequest_AmountPresent_Charges(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New()}
	ctx := WithChargeAmount(
		WithChargeStore(WithCapability(context.Background(), cap), store),
		0.001,
		"",
	)
	if err := ChargeRequest(ctx); err != nil {
		t.Fatalf("charge: %v", err)
	}
	u, _ := store.Get(ctx, cap.ID)
	if u.SpentAmount < 0.0009 || u.SpentAmount > 0.0011 {
		t.Errorf("spent = %v, want ~0.001", u.SpentAmount)
	}
}

func TestChargeRequest_NoCapability_NoOp(t *testing.T) {
	// No capability on context — JWT auth path. Even with an amount
	// stamped, ChargeRequest returns nil without touching the store.
	store := newFakeUsage()
	ctx := WithChargeAmount(
		WithChargeStore(context.Background(), store),
		0.5,
		"",
	)
	if err := ChargeRequest(ctx); err != nil {
		t.Errorf("no capability = no-op, got %v", err)
	}
}

// TestChargeCapability_NonUSDUnit_RecordsUnit covers the new
// unit_code path: a capability minted with UnitCode=EUR records its
// charge under EUR (the fakeUsage doesn't enforce a unit on its
// in-memory map, but the call signature carries it through, and
// ChargeCapability with empty `unit` arg falls back to the
// capability's declared UnitCode).
func TestChargeCapability_NonUSDUnit_RecordsUnit(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{
		ID: uuid.New(),
		Caveats: capability.Caveats{
			MaxBudgetAmount: 5.0,
			UnitCode:        "EUR",
		},
	}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)
	// Empty unit arg defers to the capability's UnitCode (EUR).
	if err := ChargeCapability(ctx, 0.50, ""); err != nil {
		t.Fatalf("EUR charge: %v", err)
	}
	u, _ := store.Get(ctx, cap.ID)
	if u.SpentAmount < 0.49 || u.SpentAmount > 0.51 {
		t.Errorf("EUR spent = %v, want ~0.50", u.SpentAmount)
	}
}

// ─── RefundCapability ───────────────────────────────────────────────

func TestRefundCapability_AfterCharge_DecrementsSpend(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.50, ""); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if err := RefundCapability(ctx, 0.30); err != nil {
		t.Fatalf("refund: %v", err)
	}
	u, _ := store.Get(ctx, cap.ID)
	if u.SpentAmount < 0.19 || u.SpentAmount > 0.21 {
		t.Errorf("spent after refund = %v, want ~0.20", u.SpentAmount)
	}
}

func TestRefundCapability_FloorsAtZero(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New()}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.10, ""); err != nil {
		t.Fatalf("charge: %v", err)
	}
	// Refund larger than current spend → floors at 0, never negative.
	if err := RefundCapability(ctx, 1.00); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if u, _ := store.Get(ctx, cap.ID); u.SpentAmount != 0 {
		t.Errorf("spent must floor at 0, got %v", u.SpentAmount)
	}
}

func TestRefundCapability_NoCapNoStore_NoOp(t *testing.T) {
	if err := RefundCapability(context.Background(), 1.0); err != nil {
		t.Errorf("no cap: %v", err)
	}
	cap := &capability.Capability{ID: uuid.New()}
	ctx := WithCapability(context.Background(), cap)
	if err := RefundCapability(ctx, 1.0); err != nil {
		t.Errorf("no store: %v", err)
	}
}
