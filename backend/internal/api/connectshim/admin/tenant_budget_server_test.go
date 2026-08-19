package admin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin-private/capability"
	pb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/admin/v1"
)

// fakeUsageStore is a minimal in-memory capability.UsageStore[pgx.Tx] that
// covers just the methods TenantBudgetServer touches. The full
// interface lives across many call sites; we mock only what's
// reached in this test file.
type fakeUsageStore struct {
	budgets map[uuid.UUID]capability.TenantBudget
	getErr  error
}

func (f *fakeUsageStore) BumpRequest(context.Context, uuid.UUID, int64) (int64, error) {
	return 0, errors.New("not used")
}
func (f *fakeUsageStore) Charge(context.Context, uuid.UUID, float64, float64, string, uuid.UUID, string, string, func(context.Context, pgx.Tx) error) (float64, error) {
	return 0, errors.New("not used")
}
func (f *fakeUsageStore) RefundCapability(context.Context, uuid.UUID, float64) error {
	return errors.New("not used")
}
func (f *fakeUsageStore) RefundTenant(context.Context, uuid.UUID, float64) error {
	return errors.New("not used")
}
func (f *fakeUsageStore) Get(context.Context, uuid.UUID) (capability.Usage, error) {
	return capability.Usage{}, errors.New("not used")
}

func (f *fakeUsageStore) GetTenantBudget(_ context.Context, id uuid.UUID) (capability.TenantBudget, error) {
	if f.getErr != nil {
		return capability.TenantBudget{}, f.getErr
	}
	tb, ok := f.budgets[id]
	if !ok {
		return capability.TenantBudget{}, capability.ErrTenantBudgetNotFound
	}
	return tb, nil
}

func (f *fakeUsageStore) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetArgs) (capability.TenantBudget, error) {
	if f.budgets == nil {
		f.budgets = map[uuid.UUID]capability.TenantBudget{}
	}
	unit := args.UnitCode
	if unit == "" {
		unit = f.budgets[args.TenantID].UnitCode
	}
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	tb := capability.TenantBudget{
		TenantID:        args.TenantID,
		MaxBudgetAmount: args.MaxBudgetAmount,
		UnitCode:        unit,
		// SpentAmount: ResetSpend semantics aren't unit-tested
		// here — the postgres impl owns the SQL that zeroes it.
		SpentAmount: f.budgets[args.TenantID].SpentAmount,
	}
	if args.ResetSpend {
		tb.SpentAmount = 0
	}
	f.budgets[args.TenantID] = tb
	return tb, nil
}

func (f *fakeUsageStore) Delete(context.Context, uuid.UUID) error {
	return errors.New("not used")
}
func (f *fakeUsageStore) PurgeOrphans(context.Context) (int64, error) {
	return 0, errors.New("not used")
}
func (f *fakeUsageStore) ListTenantBudgets(context.Context, capability.ListTenantBudgetsArgs) ([]capability.TenantBudgetSummary, error) {
	return nil, nil
}

func TestTenantBudgetServer_Get_NotFound(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	tenantID := uuid.New()
	_, err := srv.Get(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceGetRequest{
		TenantId: tenantID.String(),
	}))
	if err == nil {
		t.Fatal("expected NotFound error")
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %v", err)
	}
}

func TestTenantBudgetServer_SetThenGet_RoundTrip(t *testing.T) {
	store := &fakeUsageStore{}
	srv := NewTenantBudgetServer(store)
	tenantID := uuid.New()

	setRes, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID.String(),
		MaxBudgetAmount: 100.0,
		ResetSpend:      true,
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := setRes.Msg.GetBudget().GetMaxBudgetAmount(); got != 100.0 {
		t.Errorf("max_budget_amount: got %v, want 100", got)
	}
	if got := setRes.Msg.GetBudget().GetUnitCode(); got != capability.DefaultUnitCode {
		t.Errorf("unit_code: got %q, want %q (default)", got, capability.DefaultUnitCode)
	}

	getRes, err := srv.Get(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceGetRequest{
		TenantId: tenantID.String(),
	}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := getRes.Msg.GetBudget().GetTenantId(); got != tenantID.String() {
		t.Errorf("tenant_id: got %q, want %q", got, tenantID)
	}
}

func TestTenantBudgetServer_NilUsageStore_Unavailable(t *testing.T) {
	srv := NewTenantBudgetServer(nil)
	_, err := srv.Get(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceGetRequest{
		TenantId: uuid.New().String(),
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeUnavailable {
		t.Errorf("expected CodeUnavailable on nil store, got %v", err)
	}
}

// TestTenantBudgetServer_Set_NonUSDUnit covers the new unit_code
// path: a budget set with unit_code=EUR round-trips through
// {Set, Get} carrying the EUR designation.
func TestTenantBudgetServer_Set_NonUSDUnit(t *testing.T) {
	store := &fakeUsageStore{}
	srv := NewTenantBudgetServer(store)
	tenantID := uuid.New()

	setRes, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID.String(),
		MaxBudgetAmount: 250.0,
		UnitCode:        "EUR",
		ResetSpend:      true,
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := setRes.Msg.GetBudget().GetUnitCode(); got != "EUR" {
		t.Errorf("unit_code: got %q, want EUR", got)
	}
}

// TestTenantBudgetServer_Set_BadUnit rejects unknown unit codes at
// the boundary with CodeInvalidArgument.
func TestTenantBudgetServer_Set_BadUnit(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        uuid.New().String(),
		MaxBudgetAmount: 1.0,
		UnitCode:        "XYZ",
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument on unknown unit, got %v", err)
	}
}

func TestTenantBudgetServer_BadTenantID(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Get(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceGetRequest{
		TenantId: "not-a-uuid",
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", err)
	}
}
