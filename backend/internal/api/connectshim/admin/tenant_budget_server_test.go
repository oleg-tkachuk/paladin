package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/capability"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeUsageStore is a minimal in-memory capability.TenantBudgets — the
// only part of the usage store TenantBudgetServer depends on.
type fakeUsageStore struct {
	budgets map[uuid.UUID]capability.TenantBudget
	getErr  error
	// lastSet is the last SetTenantBudget's arguments.
	lastSet capability.SetTenantBudgetRequest
	// lastList is the last ListTenantBudgets' arguments; listNext and
	// listErr are what it answers.
	lastList capability.ListTenantBudgetsRequest
	listNext string
	listErr  error
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

func (f *fakeUsageStore) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetRequest) (capability.TenantBudget, error) {
	f.lastSet = args
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

func (f *fakeUsageStore) ListTenantBudgets(_ context.Context, req capability.ListTenantBudgetsRequest) ([]capability.TenantBudgetSummary, string, error) {
	f.lastList = req
	return nil, f.listNext, f.listErr
}

// Summarize pages through the store's cursor, both ways.
func TestTenantBudgetServer_Summarize_Pages(t *testing.T) {
	store := &fakeUsageStore{listNext: "next-page"}
	resp, err := NewTenantBudgetServer(store).Summarize(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSummarizeRequest{
		Limit: 2, PageToken: "this-page",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if store.lastList.Cursor != "this-page" || store.lastList.Limit != 2 {
		t.Errorf("store asked for %+v; want the page token and limit sent", store.lastList)
	}
	if resp.Msg.GetNextPageToken() != "next-page" {
		t.Errorf("next_page_token = %q, want the store's cursor", resp.Msg.GetNextPageToken())
	}
}

// A page token the store cannot read is the caller's to fix.
func TestTenantBudgetServer_Summarize_BadPageToken(t *testing.T) {
	store := &fakeUsageStore{listErr: fmt.Errorf("%w: cursor", capability.ErrInvalidRequest)}
	_, err := NewTenantBudgetServer(store).Summarize(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSummarizeRequest{
		PageToken: "garbage",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
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
		TenantId:   tenantID.String(),
		MaxBudget:  &money.Money{CurrencyCode: "USD", Units: 100},
		ResetSpend: true,
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := setRes.Msg.GetBudget().GetMaxBudget(), (&money.Money{CurrencyCode: capability.DefaultUnitCode, Units: 100}); !proto.Equal(got, want) {
		t.Errorf("max_budget: got %v, want %v", got, want)
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

// The request names the tenant by id, so the audit row named nothing and a
// platform admin's change to a tenant's budget was in no tenant's trail.
func TestTenantBudgetServer_Set_NamesTheBudgetForTheAuditLog(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	tenantID := uuid.New()
	ctx := apiutil.WithResourceSlot(context.Background())
	if _, err := srv.Set(ctx, connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:  tenantID.String(),
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 100},
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := apiutil.ResourceFromContext(ctx)
	if want := apiutil.TenantNamePrefix + tenantID.String() + budgetSegment; got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}
	if tenant, ok := apiutil.TenantInResourceName(got); !ok || tenant != tenantID {
		t.Errorf("the name files the row under %s (%v), want %s", tenant, ok, tenantID)
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

// TestTenantBudgetServer_Set_NonUSDUnit covers a non-default
// currency: a budget set in EUR round-trips through
// {Set, Get} carrying the EUR designation.
func TestTenantBudgetServer_Set_NonUSDUnit(t *testing.T) {
	store := &fakeUsageStore{}
	srv := NewTenantBudgetServer(store)
	tenantID := uuid.New()

	setRes, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:   tenantID.String(),
		MaxBudget:  &money.Money{CurrencyCode: "EUR", Units: 250},
		ResetSpend: true,
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := setRes.Msg.GetBudget().GetMaxBudget(), (&money.Money{CurrencyCode: "EUR", Units: 250}); !proto.Equal(got, want) {
		t.Errorf("max_budget: got %v, want %v", got, want)
	}
}

// TestTenantBudgetServer_Set_BadUnit rejects unknown unit codes at
// the boundary with CodeInvalidArgument.
func TestTenantBudgetServer_Set_BadUnit(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:  uuid.New().String(),
		MaxBudget: &money.Money{CurrencyCode: "XYZ", Units: 1},
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
		TenantId:  uuid.New().String(),
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 10},
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
		TenantId:  tenantID,
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 10},
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := srv.Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID,
		MaxBudget:       &money.Money{CurrencyCode: "USD", Units: 999},
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
		MaxBudget:       &money.Money{CurrencyCode: "USD", Units: 1},
		ResourceVersion: "not-a-number",
	}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want CodeInvalidArgument", err)
	}
}
