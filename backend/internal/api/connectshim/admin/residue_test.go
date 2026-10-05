package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Each case below fails if its condition in this package's shims is
// inverted or weakened — the survivors of an exhaustive mutation run.

// ─── converters ────────────────────────────────────────────────────────────

func TestConvertersAnswerNilWithNil(t *testing.T) {
	if bucketToProto(nil) != nil || auditEntryToProto(nil) != nil ||
		quotaToProto(nil) != nil || eventSubToProto(nil) != nil {
		t.Error("a nil domain value became a message")
	}
	if kind, cfg := sinkToConfig(nil); kind != "" || cfg != nil {
		t.Errorf("no sink: kind %q, config %s", kind, cfg)
	}
}

// A sink given is a sink used: an inverted nil guard would answer every real
// one as "no sink", and nil alone cannot tell, since the proto getter on a
// nil sink is nil-safe.
func TestSinkToConfigReadsAGivenSink(t *testing.T) {
	const url = "https://hooks.example.com/paladin"
	kind, cfg := sinkToConfig(&pb.EventSink{Target: &pb.EventSink_Http{Http: &pb.HttpSink{Url: url}}})
	if kind != "http" || !strings.Contains(string(cfg), url) {
		t.Errorf("kind %q, config %s", kind, cfg)
	}
}

func TestQuotaNameByScope(t *testing.T) {
	tenant := uuid.New()
	cases := []struct {
		name string
		q    admindomain.Quota
		want string
	}{
		{"tenant", admindomain.Quota{TenantID: tenant}, "tenants/" + tenant.String() + "/quota"},
		{"bucket", admindomain.Quota{BackendID: "b1", BucketName: "n1"}, "storageBackends/b1/buckets/n1/quota"},
		{"backend without a bucket", admindomain.Quota{BackendID: "b1"}, ""},
		{"bucket without a backend", admindomain.Quota{BucketName: "n1"}, ""},
	}
	for _, c := range cases {
		if got := quotaToProto(&c.q).GetName(); got != c.want {
			t.Errorf("%s: name %q, want %q", c.name, got, c.want)
		}
	}
}

func TestResourceNameHelpers(t *testing.T) {
	if _, err := backendIDFromName("storageBackends//x"); err == nil {
		t.Error("a backend name with an empty first segment and a child was accepted")
	}
	if got, err := backendIDFromName("storageBackends/b1"); err != nil || got != "b1" {
		t.Errorf("backendIDFromName = %q, %v", got, err)
	}
	if got, err := tenantIDFromName("tenants//eventSubscriptions/x"); err != nil || got != "" {
		t.Errorf("tenantIDFromName of an empty segment = %q, %v; want the empty segment", got, err)
	}
	if b, n := splitBucketCursor("b1/n1"); b != "b1" || n != "n1" {
		t.Errorf("splitBucketCursor = %q, %q", b, n)
	}
}

func TestTenantLabels(t *testing.T) {
	got := tenantDomainToProto(&tenanth.Tenant{Labels: []byte(`{"team":"a"}`)}).GetLabels()
	if got["team"] != "a" {
		t.Errorf("labels = %v", got)
	}
	// An unreadable blob is dropped, not half-applied. `len(Labels) > 0`
	// against `>= 0` is equivalent: Unmarshal of nothing fails the same way.
	if got := tenantDomainToProto(&tenanth.Tenant{Labels: []byte(`{`)}).GetLabels(); got != nil {
		t.Errorf("unreadable labels = %v", got)
	}
}

func TestTimeFromPB(t *testing.T) {
	if !timeFromPB(nil).IsZero() {
		t.Error("no timestamp is not the zero time")
	}
	at := time.Unix(1_767_225_600, 0).UTC()
	if got := timeFromPB(timestamppb.New(at)); !got.Equal(at) {
		t.Errorf("timeFromPB = %v, want %v", got, at)
	}
}

// ─── buckets and backends ──────────────────────────────────────────────────

func TestListBucketsNarrowsToTheParentBackend(t *testing.T) {
	h := &recordingBuckets{}
	if _, err := (&BucketServer{H: h}).ListBuckets(context.Background(),
		connect.NewRequest(&pb.ListBucketsRequest{Parent: "storageBackends/b1"})); err != nil {
		t.Fatal(err)
	}
	if h.listArgs.BackendID != "b1" {
		t.Errorf("BackendID = %q, want b1", h.listArgs.BackendID)
	}
}

type recordingBackendCreate struct {
	failingBackend
	got admindomain.StorageBackend
}

func (r *recordingBackendCreate) CreateBackend(_ context.Context, b admindomain.StorageBackend) (*admindomain.StorageBackend, error) {
	r.got = b
	return &b, nil
}

func TestCreateBackendTakesTheIDFromEitherPlace(t *testing.T) {
	for name, req := range map[string]*pb.CreateBackendRequest{
		"the request's backend_id": {BackendId: "from-request", Backend: &pb.StorageBackend{}},
		"the body's own id":        {BackendId: "from-request", Backend: &pb.StorageBackend{BackendId: "from-body"}},
	} {
		h := &recordingBackendCreate{}
		if _, err := (&BackendServer{H: h}).CreateBackend(context.Background(), connect.NewRequest(req)); err != nil {
			t.Fatal(err)
		}
		want := req.GetBackend().GetBackendId()
		if want == "" {
			want = req.GetBackendId()
		}
		if h.got.BackendID != want {
			t.Errorf("%s: id %q, want %q", name, h.got.BackendID, want)
		}
	}
}

func TestRotateCredentialsRefusesABadGracePeriod(t *testing.T) {
	_, err := (&BackendServer{H: failingBackend{}}).RotateCredentials(context.Background(),
		connect.NewRequest(&pb.RotateCredentialsRequest{Name: "storageBackends/b1", GracePeriod: "soon"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}

// ─── quotas ────────────────────────────────────────────────────────────────

func TestResetUsageRoutesByScopeAndReportsTheReset(t *testing.T) {
	tenant := uuid.New()
	h := &recordingQuota{}
	_, err := (&QuotaServer{H: h}).ResetUsage(context.Background(),
		connect.NewRequest(&pb.ResetUsageRequest{Name: "tenants/" + tenant.String() + "/quota"}))
	if h.tenantScope != tenant {
		t.Errorf("tenant scope = %v, want %v", h.tenantScope, tenant)
	}
	// The read succeeds and the reset fails: the reset's error is the answer.
	if !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want the reset's", err)
	}

	h = &recordingQuota{}
	_, _ = (&QuotaServer{H: h}).ResetUsage(context.Background(),
		connect.NewRequest(&pb.ResetUsageRequest{Name: "storageBackends/b1/buckets/n1/quota"}))
	if h.bucketScope != [2]string{"b1", "n1"} || h.tenantScope != uuid.Nil {
		t.Errorf("bucket scope = %v, tenant = %v", h.bucketScope, h.tenantScope)
	}
}

func TestParseQuotaNameNeedsTheTenantsPrefix(t *testing.T) {
	if _, err := parseQuotaName("collections/" + uuid.NewString() + "/quota"); err == nil {
		t.Error("a three-segment name that is not a tenant's was read as one")
	}
}

// ─── event subscriptions ───────────────────────────────────────────────────

type slugTenants struct{ id uuid.UUID }

func (s slugTenants) GetTenantBySlug(context.Context, string) (*tenanth.Tenant, error) {
	if s.id == uuid.Nil {
		return nil, errBoom
	}
	return &tenanth.Tenant{TenantID: s.id}, nil
}

func TestSubscriptionNameBySlug(t *testing.T) {
	sub := uuid.New()
	name := "tenants/acme/eventSubscriptions/" + sub.String()
	if _, _, err := (&EventSubscriptionServer{Tenants: slugTenants{}}).resolveSubscriptionName(context.Background(), name); !errors.Is(err, errBoom) {
		t.Errorf("an unresolvable slug: err = %v", err)
	}
	tenant := uuid.New()
	gotTenant, gotSub, err := (&EventSubscriptionServer{Tenants: slugTenants{id: tenant}}).resolveSubscriptionName(context.Background(), name)
	if err != nil || gotTenant != tenant || gotSub != sub {
		t.Errorf("resolved %v %v, %v", gotTenant, gotSub, err)
	}
}

func TestListSubscriptionsCursor(t *testing.T) {
	after := uuid.New()
	for tok, want := range map[string]uuid.UUID{after.String(): after, "not-a-uuid": uuid.Nil, "": uuid.Nil} {
		h := &recordingSubs{}
		if _, err := (&EventSubscriptionServer{H: h}).ListSubscriptions(context.Background(),
			connect.NewRequest(&pb.ListSubscriptionsRequest{Page: &commonv1.PageRequest{PageToken: tok}})); err != nil {
			t.Fatal(err)
		}
		if h.args.AfterID != want {
			t.Errorf("token %q: AfterID %v, want %v", tok, h.args.AfterID, want)
		}
	}
}

type deliveringSubs struct{ failingEventSubscription }

func (deliveringSubs) TestSubscription(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func TestTestSubscriptionReportsDelivery(t *testing.T) {
	resp, err := (&EventSubscriptionServer{H: deliveringSubs{}}).TestSubscription(context.Background(),
		connect.NewRequest(&pb.TestSubscriptionRequest{Name: "tenants/" + uuid.NewString() + "/eventSubscriptions/" + uuid.NewString()}))
	if err != nil || !resp.Msg.GetDelivered() {
		t.Fatalf("delivered = %v, %v", resp, err)
	}
}

// ─── system ────────────────────────────────────────────────────────────────

type reportingSystem struct{ failingSystem }

func (reportingSystem) DispatcherStats(context.Context) (*worker.DeliveryStats, bool, error) {
	return &worker.DeliveryStats{Pending: 3, Failed: 1}, true, nil
}

func (reportingSystem) PlatformStats(context.Context) (*systemh.PlatformStatsResult, error) {
	return &systemh.PlatformStatsResult{
		ControlPlane: &platformstats.ControlPlane{Tenants: platformstats.TenantCensus{Total: 4}},
		RLS:          &platformstats.RLSCensus{Objects: platformstats.ObjectCensus{TotalCount: 7}},
		RLSAvailable: true,
	}, nil
}

func TestSystemStatsCarryWhatTheHandlerReported(t *testing.T) {
	srv := &SystemServer{H: reportingSystem{}}
	ds, err := srv.GetDispatcherStats(context.Background(), connect.NewRequest(&pb.GetDispatcherStatsRequest{}))
	if err != nil || ds.Msg.GetPending() != 3 || ds.Msg.GetFailed() != 1 {
		t.Errorf("dispatcher stats = %v, %v", ds, err)
	}
	ps, err := srv.GetPlatformStats(context.Background(), connect.NewRequest(&pb.GetPlatformStatsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Msg.GetTenants().GetTotal() != 4 {
		t.Errorf("tenant total = %d, want 4", ps.Msg.GetTenants().GetTotal())
	}
	if ps.Msg.GetRls().GetObjects().GetTotalCount() != 7 {
		t.Errorf("object total = %d, want 7", ps.Msg.GetRls().GetObjects().GetTotalCount())
	}
}

// ─── tenant budgets ────────────────────────────────────────────────────────

func TestTenantBudgetSetForwardsThePeriodEnd(t *testing.T) {
	store := &fakeUsageStore{}
	end := time.Unix(1_767_225_600, 0).UTC()
	if _, err := NewTenantBudgetServer(store).Set(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSetRequest{
		TenantId: uuid.NewString(), PeriodEnd: timestamppb.New(end),
	})); err != nil {
		t.Fatal(err)
	}
	if store.lastSet.PeriodEnd == nil || !store.lastSet.PeriodEnd.Equal(end) {
		t.Errorf("PeriodEnd = %v, want %v", store.lastSet.PeriodEnd, end)
	}
}
