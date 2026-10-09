package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeUsageStore is a minimal in-memory limes.TenantBudgets — the
// only part of the usage store TenantBudgetServer depends on.
type fakeUsageStore struct {
	budgets map[uuid.UUID]limes.TenantBudget
	getErr  error
	// lastSet is the last SetTenantBudget's arguments.
	lastSet limes.SetTenantBudgetRequest
	// lastList is the last ListTenantBudgets' arguments; listNext and
	// listErr are what it answers.
	lastList limes.ListTenantBudgetsRequest
	listNext string
	listErr  error
}

func (f *fakeUsageStore) GetTenantBudget(_ context.Context, id uuid.UUID) (limes.TenantBudget, error) {
	if f.getErr != nil {
		return limes.TenantBudget{}, f.getErr
	}
	tb, ok := f.budgets[id]
	if !ok {
		return limes.TenantBudget{}, limes.ErrTenantBudgetNotFound
	}
	return tb, nil
}

func (f *fakeUsageStore) SetTenantBudget(_ context.Context, args limes.SetTenantBudgetRequest) (limes.TenantBudget, error) {
	f.lastSet = args
	if f.budgets == nil {
		f.budgets = map[uuid.UUID]limes.TenantBudget{}
	}
	// Mirror the store's OCC contract, or the handler test would pass against
	// a fake that accepts writes production refuses.
	prev, exists := f.budgets[args.TenantID]
	want := int64(0)
	if exists {
		want = prev.ResourceVersion
	}
	if args.ExpectedVersion != want {
		return limes.TenantBudget{}, limes.ErrTenantBudgetVersionMismatch
	}
	unit := args.UnitCode
	if unit == "" {
		unit = f.budgets[args.TenantID].UnitCode
	}
	if unit == "" {
		unit = limes.DefaultUnitCode
	}
	tb := limes.TenantBudget{
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

func (f *fakeUsageStore) ListTenantBudgets(_ context.Context, req limes.ListTenantBudgetsRequest) ([]limes.TenantBudgetSummary, string, error) {
	f.lastList = req
	return nil, f.listNext, f.listErr
}

// Summarize pages through the store's cursor, both ways.
func TestTenantBudgetServer_Summarize_Pages(t *testing.T) {
	store := &fakeUsageStore{listNext: "next-page"}
	resp, err := NewTenantBudgetServer(store).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		Limit: 2, PageToken: "this-page",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.lastList.Cursor != "this-page" || store.lastList.Limit != 2 {
		t.Errorf("store asked for %+v; want the page token and limit sent", store.lastList)
	}
	if resp.GetNextPageToken() != "next-page" {
		t.Errorf("next_page_token = %q, want the store's cursor", resp.GetNextPageToken())
	}
}

// A page token the store cannot read is the caller's to fix.
func TestTenantBudgetServer_Summarize_BadPageToken(t *testing.T) {
	store := &fakeUsageStore{listErr: fmt.Errorf("%w: cursor", limes.ErrInvalidRequest)}
	_, err := NewTenantBudgetServer(store).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		PageToken: "garbage",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestTenantBudgetServer_Get_NotFound(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	tenantID := uuid.New()
	_, err := srv.Get(context.Background(), &pb.TenantBudgetServiceGetRequest{
		TenantId: tenantID.String(),
	})
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

	setRes, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:   tenantID.String(),
		MaxBudget:  &money.Money{CurrencyCode: "USD", Units: 100},
		ResetSpend: true,
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := setRes.GetBudget().GetMaxBudget(), (&money.Money{CurrencyCode: limes.DefaultUnitCode, Units: 100}); !proto.Equal(got, want) {
		t.Errorf("max_budget: got %v, want %v", got, want)
	}

	getRes, err := srv.Get(context.Background(), &pb.TenantBudgetServiceGetRequest{
		TenantId: tenantID.String(),
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := getRes.GetBudget().GetTenantId(); got != tenantID.String() {
		t.Errorf("tenant_id: got %q, want %q", got, tenantID)
	}
}

// The request names the tenant by id, so the audit row named nothing and a
// platform admin's change to a tenant's budget was in no tenant's trail.
func TestTenantBudgetServer_Set_NamesTheBudgetForTheAuditLog(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	tenantID := uuid.New()
	ctx := apiutil.WithResourceSlot(context.Background())
	if _, err := srv.Set(ctx, &pb.TenantBudgetServiceSetRequest{
		TenantId:  tenantID.String(),
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 100},
	}); err != nil {
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
	if _, err := srv.Set(context.Background(), old); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestTenantBudgetServer_NilUsageStore_Unavailable(t *testing.T) {
	srv := NewTenantBudgetServer(nil)
	_, err := srv.Get(context.Background(), &pb.TenantBudgetServiceGetRequest{
		TenantId: uuid.New().String(),
	})
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

	setRes, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:   tenantID.String(),
		MaxBudget:  &money.Money{CurrencyCode: "EUR", Units: 250},
		ResetSpend: true,
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := setRes.GetBudget().GetMaxBudget(), (&money.Money{CurrencyCode: "EUR", Units: 250}); !proto.Equal(got, want) {
		t.Errorf("max_budget: got %v, want %v", got, want)
	}
}

// TestTenantBudgetServer_Set_BadUnit rejects unknown unit codes at
// the boundary with CodeInvalidArgument.
func TestTenantBudgetServer_Set_BadUnit(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:  uuid.New().String(),
		MaxBudget: &money.Money{CurrencyCode: "XYZ", Units: 1},
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument on unknown unit, got %v", err)
	}
}

func TestTenantBudgetServer_BadTenantID(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Get(context.Background(), &pb.TenantBudgetServiceGetRequest{
		TenantId: "not-a-uuid",
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", err)
	}
}

// The Set response has to carry the new version, or the console has nothing to
// send on the next edit and every second write is a conflict.
func TestTenantBudgetServer_Set_ReturnsNewVersion(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	res, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:  uuid.New().String(),
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 10},
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := res.GetBudget().GetResourceVersion(); got != "1" {
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

	if _, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:  tenantID,
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 10},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID,
		MaxBudget:       &money.Money{CurrencyCode: "USD", Units: 999},
		ResourceVersion: "1234",
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeAborted {
		t.Fatalf("err = %v, want CodeAborted", err)
	}
}

// An unparseable version is a malformed request, not a conflict.
func TestTenantBudgetServer_Set_BadVersionIsInvalidArgument(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:        uuid.New().String(),
		MaxBudget:       &money.Money{CurrencyCode: "USD", Units: 1},
		ResourceVersion: "not-a-number",
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want CodeInvalidArgument", err)
	}
}
