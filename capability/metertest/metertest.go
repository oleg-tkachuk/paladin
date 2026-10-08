// Package metertest checks a capability.Meter against the module's contract,
// the way testing/fstest checks an fs.FS. A Meter is the consumer's to write,
// and most of its contract lives in request fields — a store that ignores
// ExternalRef or Overrun still compiles, and then charges a repeated cost
// twice. Run it from the store's own tests:
//
//	func TestMeterContract(t *testing.T) {
//		metertest.Run(t, func(t *testing.T) metertest.Env[pgx.Tx] { … })
//	}
//
// The checks cover what a charge does at its ceilings and when it arrives
// again: rejection that moves nothing, idempotency on ExternalRef and on a
// settled reservation, ChargeByRef, OverrunRecord past every ceiling, and the
// validation of the request. They need a real ledger, not a fake.
package metertest

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Env is one store under test, fresh for each check.
type Env[TX any] struct {
	// Ctx is the context every call runs under — scoped to Tenant, if the
	// store needs that.
	Ctx context.Context
	// Usage is the store under test, with no usage recorded.
	Usage capability.UsageStore[TX]
	// Tenant is a tenant the store can charge, with no ceiling yet.
	Tenant uuid.UUID
	// NewCapability records a capability of Tenant with the given parent
	// (uuid.Nil for a root) and budget, so the Meter finds its lineage and
	// its ceiling, and returns its id.
	NewCapability func(parent uuid.UUID, maxBudget float64) (uuid.UUID, error)
}

// The ceilings every check works under: a root, a child narrower than it,
// and a tenant aggregate between the two sums.
const (
	rootBudget   = 25.0
	childBudget  = 20.0
	tenantBudget = 30.0
	// unit is the currency every amount is in.
	unit = capability.DefaultUnitCode
	// concurrentDeliveries is how many times one cost arrives at once.
	concurrentDeliveries = 20
	// tolerance absorbs a store's decimal round trip of an amount.
	tolerance = 1e-9
)

// fixture is an Env with a root, a child under it, and the tenant's ceiling set.
type fixture[TX any] struct {
	Env[TX]
	root, child uuid.UUID
}

func newFixture[TX any](t *testing.T, setup func(t *testing.T) Env[TX]) fixture[TX] {
	t.Helper()
	env := setup(t)
	f := fixture[TX]{Env: env}
	var err error
	if f.root, err = env.NewCapability(uuid.Nil, rootBudget); err != nil {
		t.Fatalf("root capability: %v", err)
	}
	if f.child, err = env.NewCapability(f.root, childBudget); err != nil {
		t.Fatalf("child capability: %v", err)
	}
	if _, err := env.Usage.SetTenantBudget(env.Ctx, capability.SetTenantBudgetRequest{
		TenantID: env.Tenant, MaxBudgetAmount: tenantBudget, UnitCode: unit,
	}); err != nil {
		t.Fatalf("tenant budget: %v", err)
	}
	return f
}

func (f fixture[TX]) charge(req capability.ChargeRequest, onCharged func(context.Context, TX) error) (capability.ChargeReceipt, error) {
	if req.CapabilityID == uuid.Nil {
		req.CapabilityID = f.child
	}
	req.TenantID, req.UnitCode = f.Tenant, unit
	if req.MaxBudget == 0 {
		req.MaxBudget = childBudget
	}
	return f.Usage.Charge(f.Ctx, req, onCharged)
}

func (f fixture[TX]) spent(t *testing.T, id uuid.UUID) float64 {
	t.Helper()
	u, err := f.Usage.GetUsage(f.Ctx, id)
	if errors.Is(err, capability.ErrUsageNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("usage of %s: %v", id, err)
	}
	return u.SpentAmount
}

func (f fixture[TX]) tenantSpent(t *testing.T) float64 {
	t.Helper()
	b, err := f.Usage.GetTenantBudget(f.Ctx, f.Tenant)
	if err != nil {
		t.Fatalf("tenant budget: %v", err)
	}
	return b.SpentAmount
}

func near(a, b float64) bool { return math.Abs(a-b) < tolerance }

// Run runs every check as a subtest, each on a fresh Env from setup.
func Run[TX any](t *testing.T, setup func(t *testing.T) Env[TX]) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, fixture[TX])
	}{
		{"RejectionMovesNothing", checkRejectionMovesNothing[TX]},
		{"ExternalRefChargesOnce", checkExternalRefChargesOnce[TX]},
		{"ExternalRefChargesOnceConcurrently", checkExternalRefConcurrent[TX]},
		{"ChargeByRef", checkChargeByRef[TX]},
		{"MalformedRequestsAreRefused", checkMalformed[TX]},
		{"OverrunRecordCrossesEveryCeiling", checkOverrunRecord[TX]},
		{"SettleReplaysAndRecordsAnOverrun", checkSettle[TX]},
		{"OverrunRecordCrossesACopyBudget", checkCopyOverrun[TX]},
		{"TenantBudgetReadsAgree", checkTenantBudgetReads[TX]},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) { c.run(t, newFixture(t, setup)) })
	}
}

func checkRejectionMovesNothing[TX any](t *testing.T, f fixture[TX]) {
	ran := false
	_, err := f.charge(capability.ChargeRequest{Amount: childBudget + 1}, func(context.Context, TX) error { ran = true; return nil })
	if !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("charge past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if ran || f.spent(t, f.child) != 0 || f.spent(t, f.root) != 0 || f.tenantSpent(t) != 0 {
		t.Fatalf("a rejected charge moved something (callback ran: %v)", ran)
	}
}

func checkExternalRefChargesOnce[TX any](t *testing.T, f fixture[TX]) {
	fanOuts := 0
	onCharged := func(context.Context, TX) error { fanOuts++; return nil }
	first, err := f.charge(capability.ChargeRequest{Amount: 2, ExternalRef: "call-1"}, onCharged)
	if err != nil || first.Replayed {
		t.Fatalf("first charge = %+v, %v", first, err)
	}
	again, err := f.charge(capability.ChargeRequest{Amount: 5, ExternalRef: "call-1"}, onCharged)
	if err != nil || !again.Replayed || again.ChargeID != first.ChargeID || !near(again.Spent, 2) {
		t.Fatalf("repeated charge = %+v, %v; want a replay of %s at spend 2", again, err, first.ChargeID)
	}
	if fanOuts != 1 {
		t.Errorf("onCharged ran %d times, want once", fanOuts)
	}
	// The name is per capability, and no name never deduplicates.
	if r, err := f.charge(capability.ChargeRequest{CapabilityID: f.root, MaxBudget: rootBudget, Amount: 1, ExternalRef: "call-1"}, nil); err != nil || r.Replayed {
		t.Errorf("same ref on another capability = %+v, %v; want a fresh charge", r, err)
	}
	for range 2 {
		if r, err := f.charge(capability.ChargeRequest{Amount: 1}, nil); err != nil || r.Replayed {
			t.Errorf("charge without a ref = %+v, %v; want a fresh charge", r, err)
		}
	}
	if got := f.spent(t, f.child); !near(got, 4) {
		t.Errorf("child spent %v, want 2 + 1 + 1", got)
	}
}

func checkExternalRefConcurrent[TX any](t *testing.T, f fixture[TX]) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		fresh   int
		charges = map[uuid.UUID]bool{}
	)
	for range concurrentDeliveries {
		wg.Go(func() {
			r, err := f.charge(capability.ChargeRequest{Amount: 2, ExternalRef: "call-1"}, nil)
			if err != nil {
				t.Errorf("charge: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			charges[r.ChargeID] = true
			if !r.Replayed {
				fresh++
			}
		})
	}
	wg.Wait()
	if fresh != 1 || len(charges) != 1 {
		t.Fatalf("%d fresh charges over %d charge ids, want one of each", fresh, len(charges))
	}
	if got := f.spent(t, f.root); !near(got, 2) {
		t.Errorf("root spent %v, want 2", got)
	}
}

func checkChargeByRef[TX any](t *testing.T, f fixture[TX]) {
	if _, err := f.Usage.ChargeByRef(f.Ctx, f.child, "call-1"); !errors.Is(err, capability.ErrChargeNotFound) {
		t.Fatalf("before any charge: err = %v, want ErrChargeNotFound", err)
	}
	r, err := f.charge(capability.ChargeRequest{Amount: 3, ExternalRef: "call-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Usage.Refund(f.Ctx, capability.RefundRequest{ChargeID: r.ChargeID, Amount: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Usage.ChargeByRef(f.Ctx, f.child, "call-1")
	if err != nil || got.ChargeID != r.ChargeID || got.CapabilityID != f.child || !near(got.Amount, 3) ||
		!near(got.Refunded, 1) || got.ExternalRef != "call-1" || got.UnitCode != unit || got.Overrun {
		t.Fatalf("ChargeByRef = %+v, %v; want charge %s of 3 with 1 refunded", got, err, r.ChargeID)
	}
	if byID, err := f.Usage.GetCharge(f.Ctx, r.ChargeID); err != nil || byID != got {
		t.Errorf("GetCharge = %+v, %v; want what ChargeByRef read, %+v", byID, err, got)
	}
	if _, err := f.Usage.GetCharge(f.Ctx, uuid.New()); !errors.Is(err, capability.ErrChargeNotFound) {
		t.Errorf("GetCharge of an unknown id: err = %v, want ErrChargeNotFound", err)
	}
	if _, err := f.Usage.ChargeByRef(f.Ctx, f.root, "call-1"); !errors.Is(err, capability.ErrChargeNotFound) {
		t.Errorf("the ref on another capability: err = %v, want ErrChargeNotFound", err)
	}
	if _, err := f.Usage.ChargeByRef(f.Ctx, f.child, ""); !errors.Is(err, capability.ErrChargeNotFound) {
		t.Errorf("an empty ref: err = %v, want ErrChargeNotFound", err)
	}
}

func checkMalformed[TX any](t *testing.T, f fixture[TX]) {
	for name, req := range map[string]capability.ChargeRequest{
		"ref too long":      {ExternalRef: strings.Repeat("x", capability.MaxExternalRefBytes+1)},
		"ref not printable": {ExternalRef: "call\n1"},
		"unknown overrun":   {Overrun: capability.OverrunRecord + 1},
	} {
		req.Amount = 1
		if _, err := f.charge(req, nil); !errors.Is(err, capability.ErrInvalidRequest) {
			t.Errorf("%s: err = %v, want ErrInvalidRequest", name, err)
		}
	}
	if _, err := f.Usage.Settle(f.Ctx, capability.SettleRequest{
		ReservationID: uuid.New(), Amount: 1, Overrun: capability.OverrunRecord + 1,
	}, nil); !errors.Is(err, capability.ErrInvalidRequest) {
		t.Errorf("settle with an unknown overrun: err = %v, want ErrInvalidRequest", err)
	}
	if f.spent(t, f.child) != 0 {
		t.Error("a refused request moved spend")
	}
}

func checkOverrunRecord[TX any](t *testing.T, f fixture[TX]) {
	// 40 crosses the child's 20, the root's 25 and the tenant's 30.
	const amount = 40.0
	if _, err := f.charge(capability.ChargeRequest{Amount: amount}, nil); err == nil {
		t.Fatal("a rejecting charge past every ceiling was accepted")
	}
	r, err := f.charge(capability.ChargeRequest{Amount: amount, Overrun: capability.OverrunRecord}, nil)
	if err != nil || !r.Overrun || r.Replayed || !near(r.Spent, amount) {
		t.Fatalf("recording charge = %+v, %v; want %v spent, overrun", r, err, amount)
	}
	if got := f.spent(t, f.root); !near(got, amount) {
		t.Errorf("root spent %v, want %v", got, amount)
	}
	if got := f.tenantSpent(t); !near(got, amount) {
		t.Errorf("tenant spent %v, want %v", got, amount)
	}
	if _, err := f.charge(capability.ChargeRequest{Amount: 0.01}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Errorf("after an overrun a rejecting charge: err = %v, want ErrBudgetExceeded", err)
	}
	if _, err := f.Usage.Reserve(f.Ctx, capability.ReserveRequest{
		CapabilityID: f.child, TenantID: f.Tenant, Amount: 0.01, MaxBudget: childBudget, UnitCode: unit,
	}); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Errorf("after an overrun a reservation: err = %v, want ErrBudgetExceeded", err)
	}
}

func checkSettle[TX any](t *testing.T, f fixture[TX]) {
	res, err := f.Usage.Reserve(f.Ctx, capability.ReserveRequest{
		CapabilityID: f.child, TenantID: f.Tenant, Amount: 10, MaxBudget: childBudget, UnitCode: unit,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	settle := func(amount float64, p capability.OverrunPolicy) (capability.ChargeReceipt, error) {
		return f.Usage.Settle(f.Ctx, capability.SettleRequest{
			ReservationID: res.ID, Amount: amount, MaxBudget: childBudget, Overrun: p,
		}, nil)
	}
	if _, err := settle(24, capability.OverrunReject); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("rejecting settle past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	r, err := settle(24, capability.OverrunRecord)
	if err != nil || !r.Overrun || !near(r.Spent, 24) {
		t.Fatalf("recording settle = %+v, %v; want 24 spent, overrun", r, err)
	}
	if u, err := f.Usage.GetUsage(f.Ctx, f.root); err != nil || u.ReservedAmount != 0 {
		t.Errorf("root after settling = %+v, %v; want nothing held", u, err)
	}
	again, err := settle(1, capability.OverrunReject)
	if err != nil || !again.Replayed || !again.Overrun || again.ChargeID != r.ChargeID || !near(again.Spent, 24) {
		t.Errorf("settling again = %+v, %v; want a replay of %s", again, err, r.ChargeID)
	}
	if err := f.Usage.Release(f.Ctx, res.ID); err != nil {
		t.Errorf("releasing a settled reservation: %v", err)
	}
	other, err := f.Usage.Reserve(f.Ctx, capability.ReserveRequest{
		CapabilityID: f.root, TenantID: f.Tenant, Amount: 0, MaxBudget: rootBudget, UnitCode: unit,
	})
	if err != nil {
		t.Fatalf("reserve nothing: %v", err)
	}
	if err := f.Usage.Release(f.Ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Usage.Settle(f.Ctx, capability.SettleRequest{ReservationID: other.ID, Amount: 0}, nil); !errors.Is(err, capability.ErrReservationNotFound) {
		t.Errorf("settling a released reservation: err = %v, want ErrReservationNotFound", err)
	}
}

// GetTenantBudget, SetTenantBudget and ListTenantBudgets read one row; each
// returns all of it.
func checkTenantBudgetReads[TX any](t *testing.T, f fixture[TX]) {
	const held = 3.0
	if _, err := f.Usage.Reserve(f.Ctx, capability.ReserveRequest{
		CapabilityID: f.child, TenantID: f.Tenant, Amount: held, MaxBudget: childBudget, UnitCode: unit,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Usage.GetTenantBudget(f.Ctx, f.Tenant)
	if err != nil || !near(got.ReservedAmount, held) || got.ResourceVersion == 0 {
		t.Fatalf("GetTenantBudget = %+v, %v; want %v held and a version", got, err, held)
	}
	set, err := f.Usage.SetTenantBudget(f.Ctx, capability.SetTenantBudgetRequest{
		TenantID: f.Tenant, MaxBudgetAmount: tenantBudget, UnitCode: unit, ExpectedVersion: got.ResourceVersion,
	})
	if err != nil || !near(set.ReservedAmount, held) || set.ResourceVersion != got.ResourceVersion+1 {
		t.Errorf("SetTenantBudget = %+v, %v; want %v held and the next version", set, err, held)
	}
	listed, err := f.Usage.ListTenantBudgets(f.Ctx, capability.ListTenantBudgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(listed, func(s capability.TenantBudgetSummary) bool { return s.TenantID == f.Tenant })
	if i < 0 {
		t.Fatalf("ListTenantBudgets left out the tenant: %+v", listed)
	}
	if b := listed[i].Budget; !near(b.ReservedAmount, held) || b.ResourceVersion != set.ResourceVersion {
		t.Errorf("listed budget = %+v; want %v held at version %d", b, held, set.ResourceVersion)
	}
}

func checkCopyOverrun[TX any](t *testing.T, f fixture[TX]) {
	copyID := []byte("copy-1")
	copies := []capability.CopyCeiling{{RevocationID: copyID, MaxBudgetMicros: capability.MicrosPerUnit}}
	if _, err := f.charge(capability.ChargeRequest{Amount: 2, Copies: copies}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("rejecting charge past the copy's budget: err = %v, want ErrBudgetExceeded", err)
	}
	r, err := f.charge(capability.ChargeRequest{Amount: 2, Copies: copies, Overrun: capability.OverrunRecord}, nil)
	if err != nil || !r.Overrun {
		t.Fatalf("recording charge = %+v, %v; want an overrun", r, err)
	}
	reader, ok := f.Usage.(capability.CopyUsageReader)
	if !ok {
		return // a Meter that does not read copies back is checked no further
	}
	got, err := reader.CopyUsage(f.Ctx, [][]byte{copyID})
	if err != nil || len(got) != 1 || !near(got[0].SpentAmount, 2) {
		t.Errorf("copy usage = %+v, %v; want 2 spent", got, err)
	}
}
