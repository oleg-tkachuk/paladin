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

func (f *fakeUsage) Charge(_ context.Context, id uuid.UUID, amount, max float64) (float64, error) {
	next := f.spent[id] + amount
	if max > 0 && next > max {
		return 0, capability.ErrBudgetExceeded
	}
	f.spent[id] = next
	return next, nil
}

func (f *fakeUsage) Get(_ context.Context, id uuid.UUID) (capability.Usage, error) {
	c, ok := f.requests[id]
	s, sok := f.spent[id]
	if !ok && !sok {
		return capability.Usage{}, capability.ErrUsageNotFound
	}
	return capability.Usage{CapabilityID: id, RequestCount: c, SpentUSD: s}, nil
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
	if err := ChargeCapability(context.Background(), 1.0); err != nil {
		t.Errorf("no capability + Charge must be no-op, got %v", err)
	}
}

func TestChargeCapability_NoStoreInContext_NoOp(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetUSD: 1.0}}
	ctx := WithCapability(context.Background(), cap)
	if err := ChargeCapability(ctx, 0.5); err != nil {
		t.Errorf("no store on context = no-op, got %v", err)
	}
}

func TestChargeCapability_RecordsAndAllows(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetUSD: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.30); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if err := ChargeCapability(ctx, 0.30); err != nil {
		t.Fatalf("second charge: %v", err)
	}
	u, _ := store.Get(ctx, cap.ID)
	if u.SpentUSD < 0.59 || u.SpentUSD > 0.61 { // float wiggle
		t.Errorf("spent = %v, want ~0.60", u.SpentUSD)
	}
}

func TestChargeCapability_OverBudget_Rejects(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetUSD: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0.80); err != nil {
		t.Fatalf("0.80 charge: %v", err)
	}
	err := ChargeCapability(ctx, 0.50) // would exceed 1.00
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
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetUSD: 0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	for i := 0; i < 100; i++ {
		if err := ChargeCapability(ctx, 100.0); err != nil {
			t.Fatalf("unlimited budget should never reject, got %v at i=%d", err, i)
		}
	}
}

func TestChargeCapability_NegativeOrZero_NoOp(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetUSD: 1.0}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0); err != nil {
		t.Errorf("zero charge must be no-op, got %v", err)
	}
	if err := ChargeCapability(ctx, -1); err != nil {
		t.Errorf("negative charge must be no-op, got %v", err)
	}
	if u, _ := store.Get(ctx, cap.ID); u.SpentUSD != 0 {
		t.Errorf("spent should be 0, got %v", u.SpentUSD)
	}
}
