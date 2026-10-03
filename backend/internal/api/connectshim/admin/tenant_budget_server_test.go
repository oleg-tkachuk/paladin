package admin

import (
	"context"
	"errors"
	"math"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/capability"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeUsageStore is a minimal in-memory capability.TenantBudgets — the
// only part of the usage store TenantBudgetServer depends on.
type fakeUsageStore struct {
	budgets map[uuid.UUID]capability.TenantBudget
	getErr  error
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
	// Mirror the store's OCC contract, or the handler test would pass against
	// a fake that accepts writes production refuses.
	prev, exists := f.budgets[args.TenantID]
	want := int64(0)
	if exists {
		want = prev.ResourceVersion
	}
	if args.ExpectedVersion != want {
		return capability.TenantBudget{}, capability.ErrTenantBudgetVersionMismatch
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
		SpentAmount:     f.budgets[args.TenantID].SpentAmount,
		ResourceVersion: want + 1,
	}
	if args.ResetSpend {
		tb.SpentAmount = 0
	}
	f.budgets[args.TenantID] = tb
	return tb, nil
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
		MaxBudgetMicros: proto.Int64(100_000_000),
		ResetSpend:      true,
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := setRes.Msg.GetBudget().GetMaxBudgetMicros(); got != 100_000_000 {
		t.Errorf("max_budget_micros: got %d, want 100000000", got)
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

// A client built before max_budget_amount was removed still sends it. Read as
// absent, the cap it set would be lifted to unlimited.
func TestTenantBudgetServer_Set_RefusesTheRemovedDouble(t *testing.T) {
	store := &fakeUsageStore{}
	srv := NewTenantBudgetServer(store)
	old := &pb.TenantBudgetServiceSetRequest{TenantId: uuid.New().String()}
	removed := old.ProtoReflect().Descriptor().ReservedRanges().Get(0)[0]
	raw, err := proto.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, removed, protowire.Fixed64Type)
	raw = protowire.AppendFixed64(raw, math.Float64bits(100))
	if err := proto.Unmarshal(raw, old); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Set(context.Background(), connect.NewRequest(old)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
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
		MaxBudgetMicros: proto.Int64(250_000_000),
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
		MaxBudgetMicros: proto.Int64(1_000_000),
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

// The Set response has to carry the new version, or the console has nothing to
// send on the next edit and every second write is a conflict.
func TestTenantBudgetServer_Set_ReturnsNewVersion(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	res, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        uuid.New().String(),
		MaxBudgetMicros: proto.Int64(10_000_000),
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := res.Msg.GetBudget().GetResourceVersion(); got != "1" {
		t.Errorf("resource_version = %q, want \"1\"", got)
	}
}

// A stale version is the caller's problem to resolve, so it must surface as
// Aborted. Internal would tell the console to retry the same doomed write and
// page whoever watches 5xx rates.
func TestTenantBudgetServer_Set_StaleVersionIsAborted(t *testing.T) {
	store := &fakeUsageStore{}
	srv := NewTenantBudgetServer(store)
	tenantID := uuid.New().String()

	if _, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID,
		MaxBudgetMicros: proto.Int64(10_000_000),
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID,
		MaxBudgetMicros: proto.Int64(999_000_000),
		ResourceVersion: "1234",
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeAborted {
		t.Fatalf("err = %v, want CodeAborted", err)
	}
}

// An unparseable version is a malformed request, not a conflict.
func TestTenantBudgetServer_Set_BadVersionIsInvalidArgument(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        uuid.New().String(),
		MaxBudgetMicros: proto.Int64(1_000_000),
		ResourceVersion: "not-a-number",
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want CodeInvalidArgument", err)
	}
}
