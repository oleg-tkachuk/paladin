package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeUsageStore is a minimal in-memory meter.TenantBudgets — the
// only part of the usage store TenantBudgetServer depends on.
type fakeUsageStore struct {
	budgets map[uuid.UUID]meter.TenantBudget
	getErr  error
	// lastSet is the last SetTenantBudget's arguments.
	lastSet meter.SetTenantBudgetRequest
	// lastList is the last ListTenantBudgets' arguments; listOut, listNext
	// and listErr are what it answers.
	lastList meter.ListTenantBudgetsRequest
	listOut  []meter.TenantBudget
	listNext string
	listErr  error
	// setErr, when set, is what SetTenantBudget answers.
	setErr error
}

func (f *fakeUsageStore) GetTenantBudget(_ context.Context, id uuid.UUID) (meter.TenantBudget, error) {
	if f.getErr != nil {
		return meter.TenantBudget{}, f.getErr
	}
	tb, ok := f.budgets[id]
	if !ok {
		return meter.TenantBudget{}, meter.ErrTenantBudgetNotFound
	}
	return tb, nil
}

func (f *fakeUsageStore) SetTenantBudget(_ context.Context, args meter.SetTenantBudgetRequest) (meter.TenantBudget, error) {
	f.lastSet = args
	if f.setErr != nil {
		return meter.TenantBudget{}, f.setErr
	}
	if f.budgets == nil {
		f.budgets = map[uuid.UUID]meter.TenantBudget{}
	}
	// Mirror the store's OCC contract, or the handler test would pass against
	// a fake that accepts writes production refuses.
	prev, exists := f.budgets[args.TenantID]
	want := int64(0)
	if exists {
		want = prev.ResourceVersion
	}
	if args.ExpectedVersion != want {
		return meter.TenantBudget{}, meter.ErrTenantBudgetVersionMismatch
	}
	// As capabilitypg.UsageStore does: a ceiling with no unit keeps the
	// tenant's, or takes the platform's on the first one.
	ceiling := args.Ceiling
	if ceiling.UnitCode == "" {
		ceiling.UnitCode = f.budgets[args.TenantID].Ceiling.UnitCode
	}
	if ceiling.UnitCode == "" {
		ceiling.UnitCode = fakePlatformUnit
	}
	tb := meter.TenantBudget{
		TenantID: args.TenantID,
		Ceiling:  ceiling,
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

// fakePlatformUnit is the platform currency the fake writes a first budget
// with no unit in.
const fakePlatformUnit = "USD"

func (f *fakeUsageStore) ListTenantBudgets(_ context.Context, req meter.ListTenantBudgetsRequest) (limes.Page[meter.TenantBudget], error) {
	f.lastList = req
	if f.listErr != nil {
		return limes.Page[meter.TenantBudget]{}, f.listErr
	}
	return limes.Page[meter.TenantBudget]{Items: f.listOut, Next: f.listNext}, nil
}

// fakeNames is a tenanth.Names over a fixed map; a tenant absent from it
// does not exist.
type fakeNames struct {
	names map[uuid.UUID]tenanth.Name
	err   error
	asked []uuid.UUID
}

func (f *fakeNames) TenantNames(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]tenanth.Name, error) {
	f.asked = ids
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[uuid.UUID]tenanth.Name, len(ids))
	for _, id := range ids {
		if n, ok := f.names[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

// newServer is a TenantBudgetServer over store with no tenant named.
func newServer(store meter.TenantBudgets) *TenantBudgetServer {
	return NewTenantBudgetServer(store, &fakeNames{})
}

// Summarize pages through the store's cursor, both ways.
func TestTenantBudgetServer_Summarize_Pages(t *testing.T) {
	store := &fakeUsageStore{listNext: "next-page"}
	resp, err := newServer(store).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
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
	_, err := newServer(store).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		PageToken: "garbage",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestTenantBudgetServer_Get_NotFound(t *testing.T) {
	srv := newServer(&fakeUsageStore{})
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

// Get answers the store failing as Unavailable, told only to retry: the
// store's text is no message of the caller's.
func TestTenantBudgetServer_Get_StoreFailureIsUnavailable(t *testing.T) {
	const storeText = "pgstore: dial tcp 10.0.0.7:5432: connection refused"
	srv := newServer(&fakeUsageStore{getErr: errors.New(storeText)})
	_, err := srv.Get(context.Background(), &pb.TenantBudgetServiceGetRequest{TenantId: uuid.New().String()})
	if connect.CodeOf(err) != connect.CodeUnavailable || strings.Contains(err.Error(), storeText) {
		t.Errorf("err = %v, want Unavailable without the store's text", err)
	}
}

// The rows Get answers by name codes limes defines.
func TestTenantBudgetReadExceptionsNameLimesCodes(t *testing.T) {
	if undefined := tenantBudgetReadExceptions.Undefined(); len(undefined) > 0 {
		t.Errorf("exceptions name %v, which limes does not define", undefined)
	}
}

func TestTenantBudgetServer_SetThenGet_RoundTrip(t *testing.T) {
	store := &fakeUsageStore{}
	srv := newServer(store)
	tenantID := uuid.New()

	setRes, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:      tenantID.String(),
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "USD", Units: 100}),
		ResetSpend:    true,
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := setRes.GetBudget().GetMaxBudget(), (&money.Money{CurrencyCode: fakePlatformUnit, Units: 100}); !proto.Equal(got, want) {
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
	srv := newServer(&fakeUsageStore{})
	tenantID := uuid.New()
	ctx := apiutil.WithResourceSlot(context.Background())
	if _, err := srv.Set(ctx, &pb.TenantBudgetServiceSetRequest{
		TenantId:      tenantID.String(),
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "USD", Units: 100}),
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
	srv := newServer(store)
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
	srv := NewTenantBudgetServer(nil, nil)
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
	srv := newServer(store)
	tenantID := uuid.New()

	setRes, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:      tenantID.String(),
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "EUR", Units: 250}),
		ResetSpend:    true,
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
	srv := newServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:      uuid.New().String(),
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "XYZ", Units: 1}),
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument on unknown unit, got %v", err)
	}
}

func TestTenantBudgetServer_BadTenantID(t *testing.T) {
	srv := newServer(&fakeUsageStore{})
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
	srv := newServer(&fakeUsageStore{})
	res, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:      uuid.New().String(),
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "USD", Units: 10}),
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
	srv := newServer(store)
	tenantID := uuid.New().String()

	if _, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:      tenantID,
		BudgetCeiling: limitOf(&money.Money{CurrencyCode: "USD", Units: 10}),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:        tenantID,
		BudgetCeiling:   limitOf(&money.Money{CurrencyCode: "USD", Units: 999}),
		ResourceVersion: "1234",
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeAborted {
		t.Fatalf("err = %v, want CodeAborted", err)
	}
}

// An unparseable version is a malformed request, not a conflict.
func TestTenantBudgetServer_Set_BadVersionIsInvalidArgument(t *testing.T) {
	srv := newServer(&fakeUsageStore{})
	_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
		TenantId:        uuid.New().String(),
		BudgetCeiling:   limitOf(&money.Money{CurrencyCode: "USD", Units: 1}),
		ResourceVersion: "not-a-number",
	})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want CodeInvalidArgument", err)
	}
}

// Set answers each refusal of the store with the status the caller can act
// on; a failure that refuses nothing is the store's, told to retry.
func TestTenantBudgetServer_Set_MapsTheStoresRefusals(t *testing.T) {
	cases := map[error]connect.Code{
		meter.ErrTenantBudgetVersionMismatch: connect.CodeAborted,
		limes.ErrUnitCodeMismatch:            connect.CodeFailedPrecondition,
		limes.ErrInvalidAmount:               connect.CodeInvalidArgument,
		errors.New("store down"):             connect.CodeUnavailable,
	}
	for storeErr, want := range cases {
		srv := newServer(&fakeUsageStore{setErr: storeErr})
		_, err := srv.Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
			TenantId:      uuid.New().String(),
			BudgetCeiling: limitOf(&money.Money{CurrencyCode: "EUR", Units: 1}),
		})
		if got := connect.CodeOf(err); got != want {
			t.Errorf("store answered %v: code = %v, want %v", storeErr, got, want)
		}
	}
}

// summaryThresholdPct is a utilisation threshold a summary asks for, and
// summaryThresholdBasisPoints the same threshold as limes compares it.
const (
	summaryThresholdPct         = 80
	summaryThresholdBasisPoints = 8000
)

// Summarize asks the store for the rows the request names: the unlimited
// tenants, the tenants at or past a threshold, or every tenant.
func TestTenantBudgetServer_Summarize_PicksTheFilter(t *testing.T) {
	cases := map[string]struct {
		req           *pb.TenantBudgetServiceSummarizeRequest
		wantFilter    meter.TenantBudgetFilter
		wantThreshold int
	}{
		"unlimited only": {&pb.TenantBudgetServiceSummarizeRequest{UnlimitedOnly: true}, meter.TenantBudgetFilterUnlimited, 0},
		"a threshold":    {&pb.TenantBudgetServiceSummarizeRequest{ThresholdPct: summaryThresholdPct}, meter.TenantBudgetFilterAtOrPast, summaryThresholdBasisPoints},
		"no filter":      {&pb.TenantBudgetServiceSummarizeRequest{}, meter.TenantBudgetFilterAll, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeUsageStore{}
			if _, err := newServer(store).Summarize(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			if store.lastList.Filter != tc.wantFilter || store.lastList.ThresholdBasisPoints != tc.wantThreshold {
				t.Errorf("store asked for %v at %d bp, want %v at %d bp",
					store.lastList.Filter, store.lastList.ThresholdBasisPoints, tc.wantFilter, tc.wantThreshold)
			}
		})
	}
}

// The budget rows a summary lists, and the tenants they belong to.
const (
	boundedSlug        = "acme"
	boundedDisplayName = "Acme Corp"
	trashedSlug        = "gone"
	// boundedCeilingUnits and boundedSpentUnits put the bounded tenant at
	// boundedUtilisationPct.
	boundedCeilingUnits   = 200
	boundedSpentUnits     = 50
	boundedUtilisationPct = 25
)

type summaryFixture struct {
	bounded, unlimited, trashed, unknown uuid.UUID
	store                                *fakeUsageStore
	names                                *fakeNames
}

func newSummaryFixture() summaryFixture {
	f := summaryFixture{bounded: uuid.New(), unlimited: uuid.New(), trashed: uuid.New(), unknown: uuid.New()}
	f.store = &fakeUsageStore{listOut: []meter.TenantBudget{
		{
			TenantID:    f.bounded,
			Ceiling:     limes.BudgetCeiling{Max: boundedCeilingUnits * limes.NanosPerUnit, UnitCode: fakePlatformUnit},
			SpentAmount: boundedSpentUnits * limes.NanosPerUnit,
		},
		{TenantID: f.unlimited, Ceiling: limes.BudgetCeiling{Unlimited: true, UnitCode: fakePlatformUnit}},
		{TenantID: f.trashed, Ceiling: limes.BudgetCeiling{Unlimited: true, UnitCode: fakePlatformUnit}},
		{TenantID: f.unknown, Ceiling: limes.BudgetCeiling{Unlimited: true, UnitCode: fakePlatformUnit}},
	}}
	f.names = &fakeNames{names: map[uuid.UUID]tenanth.Name{
		f.bounded:   {Slug: boundedSlug, DisplayName: boundedDisplayName},
		f.unlimited: {Slug: "open"},
		f.trashed:   {Slug: trashedSlug, Deleted: true},
	}}
	return f
}

// Summarize names each row's tenant itself — the store's rows carry only
// the id — and reports its utilisation, 0 for an unlimited tenant.
func TestTenantBudgetServer_Summarize_JoinsTenantNames(t *testing.T) {
	f := newSummaryFixture()
	resp, err := NewTenantBudgetServer(f.store, f.names).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*pb.TenantBudgetSummary{}
	for _, s := range resp.GetSummaries() {
		got[s.GetTenantId()] = s
	}
	if len(got) != len(f.store.listOut) {
		t.Fatalf("summaries = %v; want every row the store listed", resp.GetSummaries())
	}
	if len(f.names.asked) != len(f.store.listOut) {
		t.Errorf("names asked for %v; want the page's tenants", f.names.asked)
	}
	b := got[f.bounded.String()]
	if b.GetSlug() != boundedSlug || b.GetDisplayName() != boundedDisplayName {
		t.Errorf("bounded tenant named %q / %q", b.GetSlug(), b.GetDisplayName())
	}
	if b.GetUtilisationPct() != boundedUtilisationPct {
		t.Errorf("bounded utilisation = %v, want %v", b.GetUtilisationPct(), boundedUtilisationPct)
	}
	if want := (&money.Money{CurrencyCode: fakePlatformUnit, Units: boundedCeilingUnits}); !proto.Equal(b.GetBudget().GetMaxBudget(), want) {
		t.Errorf("bounded max_budget = %v, want %v", b.GetBudget().GetMaxBudget(), want)
	}
	if u := got[f.unlimited.String()]; u.GetUtilisationPct() != 0 {
		t.Errorf("unlimited utilisation = %v, want 0", u.GetUtilisationPct())
	}
	// A tenant nobody knows is still listed when nothing asks to drop it.
	if u := got[f.unknown.String()]; u.GetSlug() != "" {
		t.Errorf("unknown tenant named %q", u.GetSlug())
	}
}

// exclude_inactive drops the rows of tenants in the trash and of tenants
// that no longer exist, and keeps the store's cursor.
func TestTenantBudgetServer_Summarize_ExcludesInactiveTenants(t *testing.T) {
	f := newSummaryFixture()
	f.store.listNext = "next-page"
	resp, err := NewTenantBudgetServer(f.store, f.names).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		ExcludeInactive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, s := range resp.GetSummaries() {
		kept[s.GetTenantId()] = true
	}
	if len(kept) != 2 || !kept[f.bounded.String()] || !kept[f.unlimited.String()] {
		t.Errorf("kept %v; want only the bounded and unlimited tenants", kept)
	}
	if resp.GetNextPageToken() != f.store.listNext {
		t.Errorf("next_page_token = %q, want the store's cursor", resp.GetNextPageToken())
	}
}

// A failure to read the names is the server's, not the caller's.
func TestTenantBudgetServer_Summarize_NamesFailureIsInternal(t *testing.T) {
	f := newSummaryFixture()
	f.names.err = errors.New("db down")
	_, err := NewTenantBudgetServer(f.store, f.names).Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
}

// A zero limit allows no spend, and "no limit" is its own arm; a request that
// sets neither is refused rather than read as either.
func TestTenantBudgetServer_Set_Ceilings(t *testing.T) {
	cases := map[string]struct {
		ceiling *pb.TenantBudgetServiceSetRequest
		want    limes.BudgetCeiling
	}{
		"zero allows nothing": {&pb.TenantBudgetServiceSetRequest{BudgetCeiling: limitOf(&money.Money{CurrencyCode: fakePlatformUnit})}, limes.BudgetCeiling{UnitCode: fakePlatformUnit}},
		"bounded": {
			&pb.TenantBudgetServiceSetRequest{BudgetCeiling: limitOf(&money.Money{CurrencyCode: fakePlatformUnit, Units: boundedCeilingUnits})},
			limes.BudgetCeiling{Max: boundedCeilingUnits * limes.NanosPerUnit, UnitCode: fakePlatformUnit},
		},
		// A ceiling that names no unit reaches the store without one, which
		// keeps the tenant's (capabilitypg.UsageStore).
		"unlimited": {&pb.TenantBudgetServiceSetRequest{BudgetCeiling: unlimitedIn("")}, limes.BudgetCeiling{Unlimited: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeUsageStore{}
			req := tc.ceiling
			req.TenantId = uuid.New().String()
			res, err := newServer(store).Set(context.Background(), req)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if store.lastSet.Ceiling != tc.want {
				t.Errorf("ceiling = %s, want %s", store.lastSet.Ceiling, tc.want)
			}
			// The ceiling reads back on its own arm.
			got := res.GetBudget()
			if tc.want.Unlimited != (got.GetUnlimitedBudget() != nil) || tc.want.Unlimited == (got.GetMaxBudget() != nil) {
				t.Errorf("budget ceiling = %v, want unlimited=%v", got.GetBudgetCeiling(), tc.want.Unlimited)
			}
		})
	}
}

func TestTenantBudgetServer_Set_RefusesAnUnsetCeiling(t *testing.T) {
	store := &fakeUsageStore{}
	_, err := newServer(store).Set(context.Background(), &pb.TenantBudgetServiceSetRequest{TenantId: uuid.New().String()})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
	if store.lastSet.TenantID != uuid.Nil() {
		t.Errorf("the store was written: %+v", store.lastSet)
	}
}

// An unlimited budget that names no unit keeps the tenant's; one that names a
// unit sets it.
func TestTenantBudgetServer_Set_UnlimitedUnit(t *testing.T) {
	for unit, want := range map[string]string{"": "", "EUR": "EUR"} {
		store := &fakeUsageStore{}
		if _, err := newServer(store).Set(context.Background(), &pb.TenantBudgetServiceSetRequest{
			TenantId: uuid.New().String(), BudgetCeiling: unlimitedIn(unit),
		}); err != nil {
			t.Fatalf("%q: %v", unit, err)
		}
		if store.lastSet.Ceiling.UnitCode != want {
			t.Errorf("%q: unit = %q, want %q", unit, store.lastSet.Ceiling.UnitCode, want)
		}
	}
}

// limitOf is a Set request's budget limit.
func limitOf(m *money.Money) *pb.TenantBudgetServiceSetRequest_MaxBudget {
	return &pb.TenantBudgetServiceSetRequest_MaxBudget{MaxBudget: m}
}

// unlimitedIn is a Set request's unlimited budget in unit.
func unlimitedIn(unit string) *pb.TenantBudgetServiceSetRequest_UnlimitedBudget {
	return &pb.TenantBudgetServiceSetRequest_UnlimitedBudget{UnlimitedBudget: &pb.UnlimitedBudget{CurrencyCode: unit}}
}

// A percentage threshold reaches limes in basis points, to the nearest one.
func TestBasisPointsOf(t *testing.T) {
	for pct, want := range map[float64]int{0: 0, 1.1: 110, 29: 2900, 33.33: 3333, 100: meter.BasisPointsPerWhole} {
		if got := basisPointsOf(pct); got != want {
			t.Errorf("basisPointsOf(%v) = %d, want %d", pct, got, want)
		}
	}
}
