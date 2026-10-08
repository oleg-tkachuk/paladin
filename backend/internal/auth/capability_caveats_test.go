package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/capability"
)

// fakeUsage is an in-memory UsageStore for the caveat tests. Keeps a
// per-cap counter and spend; emulates the SQL UPSERT-and-check
// semantics by computing post-increment, comparing to the cap, and
// returning the right sentinel.
type fakeUsage struct {
	requests map[uuid.UUID]int64
	spent    map[uuid.UUID]capability.Nanos
	charges  map[uuid.UUID]fakeCharge
	reserved map[uuid.UUID]capability.Nanos
	holds    map[uuid.UUID]fakeCharge
}

type fakeCharge struct {
	capID            uuid.UUID
	amount, refunded capability.Nanos
}

func newFakeUsage() *fakeUsage {
	return &fakeUsage{
		requests: map[uuid.UUID]int64{},
		spent:    map[uuid.UUID]capability.Nanos{},
		charges:  map[uuid.UUID]fakeCharge{},
		reserved: map[uuid.UUID]capability.Nanos{},
		holds:    map[uuid.UUID]fakeCharge{},
	}
}

func (f *fakeUsage) Bump(_ context.Context, req capability.BumpRequest) (int64, error) {
	next := f.requests[req.CapabilityID] + 1
	if req.MaxRequests > 0 && next > req.MaxRequests {
		return 0, capability.ErrRequestLimitExceeded
	}
	f.requests[req.CapabilityID] = next
	return next, nil
}

func (f *fakeUsage) Charge(_ context.Context, req capability.ChargeRequest, _ func(context.Context, pgx.Tx) error) (capability.ChargeReceipt, error) {
	next := f.spent[req.CapabilityID] + req.Amount
	if req.MaxBudget > 0 && next+f.reserved[req.CapabilityID] > req.MaxBudget {
		return capability.ChargeReceipt{}, capability.ErrBudgetExceeded
	}
	f.spent[req.CapabilityID] = next
	id := uuid.New()
	f.charges[id] = fakeCharge{capID: req.CapabilityID, amount: req.Amount}
	return capability.ChargeReceipt{ChargeID: id, Spent: next}, nil
}

func (f *fakeUsage) Refund(_ context.Context, req capability.RefundRequest) (capability.Nanos, error) {
	c, ok := f.charges[req.ChargeID]
	if !ok {
		return 0, capability.ErrChargeNotFound
	}
	amount := req.Amount
	if amount == 0 {
		amount = c.amount - c.refunded
	}
	if amount > c.amount-c.refunded {
		return 0, capability.ErrRefundExceedsCharge
	}
	c.refunded += amount
	f.charges[req.ChargeID] = c
	f.spent[c.capID] = max(0, f.spent[c.capID]-amount)
	return amount, nil
}

func (f *fakeUsage) GetUsage(_ context.Context, id uuid.UUID) (capability.Usage, error) {
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

func (f *fakeUsage) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetRequest) (capability.TenantBudget, error) {
	return capability.TenantBudget{TenantID: args.TenantID, MaxBudgetAmount: args.MaxBudgetAmount, UnitCode: args.UnitCode}, nil
}

func (f *fakeUsage) ListTenantBudgets(_ context.Context, _ capability.ListTenantBudgetsRequest) ([]capability.TenantBudgetSummary, string, error) {
	return nil, "", nil
}

func (f *fakeUsage) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.requests, id)
	delete(f.spent, id)
	return nil
}

func (f *fakeUsage) PurgeOrphans(_ context.Context) (int64, error) { return 0, nil }

// ─── ChargeCapability ───────────────────────────────────────────────

func TestChargeCapability_NoCapability_NoOp(t *testing.T) {
	if err := ChargeCapability(context.Background(), capability.MustParseAmount("1.0"), ""); err != nil {
		t.Errorf("no capability + Charge must be no-op, got %v", err)
	}
}

func TestChargeCapability_NoStoreInContext_NoOp(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: capability.MustParseAmount("1.0")}}
	ctx := WithCapability(context.Background(), cap)
	if err := ChargeCapability(ctx, capability.MustParseAmount("0.5"), ""); err != nil {
		t.Errorf("no store on context = no-op, got %v", err)
	}
}

func TestChargeCapability_RecordsAndAllows(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: capability.MustParseAmount("1.0")}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, capability.MustParseAmount("0.30"), ""); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if err := ChargeCapability(ctx, capability.MustParseAmount("0.30"), ""); err != nil {
		t.Fatalf("second charge: %v", err)
	}
	u, _ := store.GetUsage(ctx, cap.ID)
	if want := capability.MustParseAmount("0.60"); u.SpentAmount != want {
		t.Errorf("spent = %v, want %v", u.SpentAmount, want)
	}
}

func TestChargeCapability_OverBudget_Rejects(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: capability.MustParseAmount("1.0")}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, capability.MustParseAmount("0.80"), ""); err != nil {
		t.Fatalf("0.80 charge: %v", err)
	}
	err := ChargeCapability(ctx, capability.MustParseAmount("0.50"), "") // would exceed 1.00
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
		if err := ChargeCapability(ctx, capability.MustParseAmount("100.0"), ""); err != nil {
			t.Fatalf("unlimited budget should never reject, got %v at i=%d", err, i)
		}
	}
}

func TestChargeCapability_NegativeOrZero_NoOp(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: capability.MustParseAmount("1.0")}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)

	if err := ChargeCapability(ctx, 0, ""); err != nil {
		t.Errorf("zero charge must be no-op, got %v", err)
	}
	if err := ChargeCapability(ctx, -capability.NanosPerUnit, ""); err != nil {
		t.Errorf("negative charge must be no-op, got %v", err)
	}
	if u, _ := store.GetUsage(ctx, cap.ID); u.SpentAmount != 0 {
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
	if u, _ := store.GetUsage(ctx, cap.ID); u.SpentAmount != 0 {
		t.Errorf("spent should stay 0, got %v", u.SpentAmount)
	}
}

func TestChargeRequest_AmountPresent_Charges(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New()}
	ctx := WithChargeAmount(
		WithChargeStore(WithCapability(context.Background(), cap), store),
		capability.MustParseAmount("0.001"),
		"",
	)
	if err := ChargeRequest(ctx); err != nil {
		t.Fatalf("charge: %v", err)
	}
	u, _ := store.GetUsage(ctx, cap.ID)
	if want := capability.MustParseAmount("0.001"); u.SpentAmount != want {
		t.Errorf("spent = %v, want %v", u.SpentAmount, want)
	}
}

func TestChargeRequest_NoCapability_NoOp(t *testing.T) {
	// No capability on context — JWT auth path. Even with an amount
	// stamped, ChargeRequest returns nil without touching the store.
	store := newFakeUsage()
	ctx := WithChargeAmount(
		WithChargeStore(context.Background(), store),
		capability.MustParseAmount("0.5"),
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
			MaxBudgetAmount: capability.MustParseAmount("5.0"),
			UnitCode:        "EUR",
		},
	}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), store)
	// Empty unit arg defers to the capability's UnitCode (EUR).
	if err := ChargeCapability(ctx, capability.MustParseAmount("0.50"), ""); err != nil {
		t.Fatalf("EUR charge: %v", err)
	}
	u, _ := store.GetUsage(ctx, cap.ID)
	if want := capability.MustParseAmount("0.50"); u.SpentAmount != want {
		t.Errorf("EUR spent = %v, want %v", u.SpentAmount, want)
	}
}

// ─── RefundLastCharge ───────────────────────────────────────────────

// chargedContext is a request context as the interceptor builds it: the
// per-request holders installed, so a charge is remembered for refunding.
func chargedContext(t *testing.T, cap *capability.Capability, store *fakeUsage, amount capability.Nanos) context.Context {
	t.Helper()
	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), cap), store))
	if err := ChargeCapability(ctx, amount, ""); err != nil {
		t.Fatalf("charge: %v", err)
	}
	return ctx
}

func TestRefundLastCharge_PartialThenRest(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{MaxBudgetAmount: capability.MustParseAmount("1.0")}}
	ctx := chargedContext(t, cap, store, capability.MustParseAmount("0.50"))

	if err := RefundLastCharge(ctx, capability.MustParseAmount("0.30")); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if u, _ := store.GetUsage(ctx, cap.ID); u.SpentAmount != capability.MustParseAmount("0.20") {
		t.Errorf("spent after partial refund = %v, want 0.20", u.SpentAmount)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("refund of the rest: %v", err)
	}
	if u, _ := store.GetUsage(ctx, cap.ID); u.SpentAmount != 0 {
		t.Errorf("spent after full refund = %v, want 0", u.SpentAmount)
	}
}

// A refund is tied to the charge it returns, so it can never give back more
// than was taken — the old counter decrement was clamped at zero, but a
// repeated refund still credited spend that other charges had made.
func TestRefundLastCharge_CannotExceedTheCharge(t *testing.T) {
	store := newFakeUsage()
	cap := &capability.Capability{ID: uuid.New()}
	ctx := chargedContext(t, cap, store, capability.MustParseAmount("0.10"))

	err := RefundLastCharge(ctx, capability.MustParseAmount("1.00"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, capability.ErrRefundExceedsCharge) {
		t.Fatalf("over-refund err = %v, want InvalidArgument wrapping ErrRefundExceedsCharge", err)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("full refund: %v", err)
	}
	if err := RefundLastCharge(ctx, 0); err != nil {
		t.Fatalf("repeated full refund must be a no-op, got %v", err)
	}
}

func TestRefundLastCharge_NoCapNoStoreNoCharge_NoOp(t *testing.T) {
	if err := RefundLastCharge(context.Background(), capability.MustParseAmount("1.0")); err != nil {
		t.Errorf("no cap: %v", err)
	}
	cap := &capability.Capability{ID: uuid.New()}
	ctx := WithCapability(context.Background(), cap)
	if err := RefundLastCharge(ctx, capability.MustParseAmount("1.0")); err != nil {
		t.Errorf("no store: %v", err)
	}
	ctx = withLastOpHolder(WithChargeStore(ctx, newFakeUsage()))
	if err := RefundLastCharge(ctx, capability.MustParseAmount("1.0")); err != nil {
		t.Errorf("no charge yet: %v", err)
	}
}

func (f *fakeUsage) Reserve(_ context.Context, req capability.ReserveRequest) (capability.Reservation, error) {
	if req.MaxBudget > 0 && f.spent[req.CapabilityID]+f.reserved[req.CapabilityID]+req.Amount > req.MaxBudget {
		return capability.Reservation{}, capability.ErrBudgetExceeded
	}
	f.reserved[req.CapabilityID] += req.Amount
	id := uuid.New()
	f.holds[id] = fakeCharge{capID: req.CapabilityID, amount: req.Amount}
	return capability.Reservation{ID: id}, nil
}

func (f *fakeUsage) Settle(ctx context.Context, req capability.SettleRequest, onCharged func(context.Context, pgx.Tx) error) (capability.ChargeReceipt, error) {
	h, ok := f.holds[req.ReservationID]
	if !ok {
		return capability.ChargeReceipt{}, capability.ErrReservationNotFound
	}
	f.reserved[h.capID] -= h.amount
	receipt, err := f.Charge(ctx, capability.ChargeRequest{CapabilityID: h.capID, Amount: req.Amount, MaxBudget: req.MaxBudget}, onCharged)
	if err != nil {
		f.reserved[h.capID] += h.amount
		return receipt, err
	}
	delete(f.holds, req.ReservationID)
	return receipt, nil
}

func (f *fakeUsage) Release(_ context.Context, id uuid.UUID) error {
	if h, ok := f.holds[id]; ok {
		f.reserved[h.capID] -= h.amount
		delete(f.holds, id)
	}
	return nil
}

func (f *fakeUsage) ReleaseExpired(context.Context) (int64, error) { return 0, nil }

func (f *fakeUsage) GetReservation(_ context.Context, id uuid.UUID) (capability.Reservation, error) {
	h, ok := f.holds[id]
	if !ok {
		return capability.Reservation{}, capability.ErrReservationNotFound
	}
	return capability.Reservation{ID: id, CapabilityID: h.capID, Amount: h.amount}, nil
}

func (f *fakeUsage) ListReservations(ctx context.Context, capID uuid.UUID) ([]capability.Reservation, error) {
	out := []capability.Reservation{}
	for id, h := range f.holds {
		if h.capID == capID {
			r, _ := f.GetReservation(ctx, id)
			out = append(out, r)
		}
	}
	return out, nil
}

// ChargeByRef finds nothing: the interceptor never charges under an external
// ref, so this fake keeps none.
func (f *fakeUsage) GetCharge(_ context.Context, id uuid.UUID) (capability.ChargeRecord, error) {
	c, ok := f.charges[id]
	if !ok {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	return capability.ChargeRecord{ChargeID: id, CapabilityID: c.capID, Amount: c.amount, Refunded: c.refunded}, nil
}

func (f *fakeUsage) ChargeByRef(context.Context, uuid.UUID, string) (capability.ChargeRecord, error) {
	return capability.ChargeRecord{}, capability.ErrChargeNotFound
}
