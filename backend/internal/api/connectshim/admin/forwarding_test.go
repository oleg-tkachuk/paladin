package admin

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/v1/collection"
	policyh "github.com/oleg-tkachuk/paladin/backend/internal/api/v1/policy"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/tenant"

	commonv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/common/v1"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
)

// Optional request fields — a tenant filter, a page cursor, a bucket scope —
// each sit behind an "if the caller supplied it" branch. A shim that dropped
// one answers with a valid-looking result computed from the wrong arguments:
// the unfiltered listing, the first page again, the tenant's quota instead of
// the bucket's. There is no error to notice.
//
// Inverting each of these conditions survived a mutation run, because nothing
// looked at what the shim passed down. These doubles record it.

type recordingBuckets struct {
	failingBucket
	listArgs   admindomain.ListBucketsArgs
	updateArgs bucketh.UpdateBucketInput
}

func (r *recordingBuckets) ListBuckets(_ context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	r.listArgs = args
	return nil, "", nil
}

func (r *recordingBuckets) UpdateBucket(_ context.Context, in bucketh.UpdateBucketInput) (*admindomain.Bucket, error) {
	r.updateArgs = in
	return &admindomain.Bucket{}, nil
}

func TestListBuckets_ForwardsTheTenantFilterAndCursor(t *testing.T) {
	owner := uuid.New()
	h := &recordingBuckets{}
	srv := &BucketServer{H: h}

	_, err := srv.ListBuckets(context.Background(), connect.NewRequest(&pb.ListBucketsRequest{
		Parent:        "storageBackends/primary",
		OwnerTenantId: owner.String(),
		Page:          &commonv1.PageRequest{PageToken: "primary/b1"},
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.listArgs.OwnerTenantID == nil || *h.listArgs.OwnerTenantID != owner {
		t.Errorf("owner_tenant_id did not reach the handler (got %v) — the caller "+
			"asked for one tenant's buckets and would be shown every tenant's",
			h.listArgs.OwnerTenantID)
	}
	if h.listArgs.AfterBackend == "" && h.listArgs.AfterName == "" {
		t.Error("the page token did not reach the handler — paging returns the " +
			"first page forever, which reads as a short list rather than a bug")
	}
}

func TestUpdateBucket_ForwardsTheOwnerTenant(t *testing.T) {
	owner := uuid.New()
	h := &recordingBuckets{}
	srv := &BucketServer{H: h}

	_, err := srv.UpdateBucket(context.Background(), connect.NewRequest(&pb.UpdateBucketRequest{
		Name: "storageBackends/primary/buckets/b1", ResourceVersion: "1",
		Bucket: &pb.Bucket{OwnerTenantId: owner.String()},
	}))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if h.updateArgs.Bucket.OwnerTenantID != owner {
		t.Errorf("owner_tenant_id did not reach the handler (got %v) — the bucket "+
			"keeps its previous owner and the response says otherwise",
			h.updateArgs.Bucket.OwnerTenantID)
	}
}

type recordingCollections struct {
	failingCollection
	listArgs   objectkey.ListCollectionsArgs
	createArgs objectkey.CreateCollectionArgs
}

func (r *recordingCollections) ListCollections(_ context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error) {
	r.listArgs = args
	return nil, "", nil
}

func (r *recordingCollections) CreateCollection(_ context.Context, args objectkey.CreateCollectionArgs) (*objectkey.Collection, error) {
	r.createArgs = args
	return &objectkey.Collection{}, nil
}

func TestListCollections_ForwardsTheBucketScope(t *testing.T) {
	tenantID := uuid.New()
	h := &recordingCollections{}
	srv := &CollectionServer{H: h, bindings: okBindings{}}

	_, err := srv.ListCollections(context.Background(), connect.NewRequest(&pb.ListCollectionsRequest{
		Parent: "tenants/" + tenantID.String(),
		Bucket: "storageBackends/primary/buckets/b1",
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.listArgs.TenantID != tenantID {
		t.Errorf("parent did not reach the handler: %v", h.listArgs.TenantID)
	}
	if h.listArgs.BackendID != "primary" || h.listArgs.BucketName != "b1" {
		t.Errorf("bucket scope did not reach the handler (%q/%q) — the storage "+
			"browser would show every collection in the tenant as if it lived "+
			"in this bucket", h.listArgs.BackendID, h.listArgs.BucketName)
	}
}

// An explicitly named bucket must be used as given. bucketRef returns early on
// an empty name; if that guard inverted, a NAMED bucket would decode to the
// empty pair and CreateCollection would silently route the collection to the
// tenant's default binding instead — a different bucket, reported as success.
func TestCreateCollection_KeepsAnExplicitBucket(t *testing.T) {
	h := &recordingCollections{}
	srv := &CollectionServer{H: h, bindings: okBindings{}}

	_, err := srv.CreateCollection(context.Background(), connect.NewRequest(&pb.CreateCollectionRequest{
		Parent: "tenants/" + uuid.NewString(), Collection: "c1",
		CollectionResource: &pb.Collection{Bucket: "storageBackends/other/buckets/b2"},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.createArgs.BackendID != "other" || h.createArgs.BucketName != "b2" {
		t.Errorf("the collection was created in %q/%q, not the bucket the caller "+
			"named — okBindings answers primary/b1, so this is the default "+
			"binding overriding an explicit choice",
			h.createArgs.BackendID, h.createArgs.BucketName)
	}
}

type recordingPolicy struct {
	failingPolicy
	in policyh.SimulateAuthzInput
}

func (r *recordingPolicy) SimulateAuthz(_ context.Context, in policyh.SimulateAuthzInput) (*policyh.SimulateAuthzOutput, error) {
	r.in = in
	return &policyh.SimulateAuthzOutput{}, nil
}

func TestSimulateAuthz_ForwardsThePrincipalTenant(t *testing.T) {
	tenantID := uuid.New()
	h := &recordingPolicy{}
	srv := &PolicyServer{H: h}

	_, err := srv.SimulateAuthz(context.Background(), connect.NewRequest(&pb.SimulateAuthzRequest{
		ResourceName: "tenants/" + tenantID.String() + "/collections/c1",
		Action:       "read", PrincipalSubject: "tester",
		PrincipalTenantId: tenantID.String(),
	}))
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if h.in.PrincipalTenantID != tenantID {
		t.Errorf("principal_tenant_id did not reach the handler (%v) — the "+
			"simulation answers for a principal in no tenant, which is not the "+
			"question that was asked", h.in.PrincipalTenantID)
	}
}

type recordingQuota struct {
	failingQuota
	bucketScope [2]string
	tenantScope uuid.UUID
}

func (r *recordingQuota) GetBucketQuota(_ context.Context, backendID, bucketName string) (*admindomain.Quota, error) {
	r.bucketScope = [2]string{backendID, bucketName}
	return &admindomain.Quota{}, nil
}

func (r *recordingQuota) GetTenantQuota(_ context.Context, tenantID uuid.UUID) (*admindomain.Quota, error) {
	r.tenantScope = tenantID
	return &admindomain.Quota{}, nil
}

// Quota names come in two shapes and route to two different handler calls.
// Only the tenant shape was exercised, so both the scope test and the
// bucket-name parse were unheld — a bucket quota answered with the tenant's
// is a plausible number in the wrong units of account.
func TestGetQuota_RoutesByScope(t *testing.T) {
	tenantID := uuid.New()

	t.Run("bucket scope", func(t *testing.T) {
		h := &recordingQuota{}
		srv := &QuotaServer{H: h}
		_, err := srv.GetQuota(context.Background(), connect.NewRequest(&pb.GetQuotaRequest{
			Name: "storageBackends/primary/buckets/b1/quota",
		}))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if h.bucketScope != [2]string{"primary", "b1"} {
			t.Errorf("bucket quota routed to %v", h.bucketScope)
		}
		if h.tenantScope != uuid.Nil {
			t.Error("a bucket-scoped name reached the tenant-quota call")
		}
	})

	t.Run("tenant scope", func(t *testing.T) {
		h := &recordingQuota{}
		srv := &QuotaServer{H: h}
		_, err := srv.GetQuota(context.Background(), connect.NewRequest(&pb.GetQuotaRequest{
			Name: "tenants/" + tenantID.String() + "/quota",
		}))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if h.tenantScope != tenantID {
			t.Errorf("tenant quota routed to %v", h.tenantScope)
		}
		if h.bucketScope != [2]string{} {
			t.Error("a tenant-scoped name reached the bucket-quota call")
		}
	})
}

// The two converters guard against a nil domain object. Production handlers
// return ErrNotFound rather than (nil, nil), so the guard is defensive — but
// inverting it turns a defensive branch into a nil dereference, and these two
// lines are the whole of the proof that it is a guard at all.
func TestDomainConvertersTolerateNil(t *testing.T) {
	if got := collectionDomainToProto(nil); got != nil {
		t.Errorf("collectionDomainToProto(nil) = %v, want nil", got)
	}
	if got := tenantDomainToProto(nil); got != nil {
		t.Errorf("tenantDomainToProto(nil) = %v, want nil", got)
	}
}

// A cursor that decodes to the zero value is a cursor that pages forever from
// the start. decodeAuditCursor answers zero for anything it cannot read, so
// the difference between "unreadable" and "read correctly" is invisible unless
// something checks what came out — inverting either parse check survived.
type recordingAudit struct {
	failingAudit
	args admindomain.ListAuditArgs
}

func (r *recordingAudit) ListAuditLog(_ context.Context, args admindomain.ListAuditArgs, _ string) ([]admindomain.AuditEntry, string, error) {
	r.args = args
	return nil, "", nil
}

func TestListAuditLog_DecodesTheCursor(t *testing.T) {
	at := time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC)
	id := uuid.New()
	h := &recordingAudit{}
	srv := &AuditServer{H: h}

	_, err := srv.ListAuditLog(context.Background(), connect.NewRequest(&pb.ListAuditLogRequest{
		Page: &commonv1.PageRequest{PageToken: at.Format(time.RFC3339Nano) + "/" + id.String()},
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if !h.args.AfterAt.Equal(at) || h.args.AfterID != id {
		t.Errorf("cursor decoded to (%v, %v), want (%v, %v) — an unreadable cursor "+
			"silently becomes the zero value, so paging restarts at the top and "+
			"the reader sees the same entries again",
			h.args.AfterAt, h.args.AfterID, at, id)
	}
}

type recordingBucketCreate struct {
	failingBucket
	in bucketh.CreateBucketInput
}

func (r *recordingBucketCreate) CreateBucket(_ context.Context, in bucketh.CreateBucketInput) (*admindomain.Bucket, error) {
	r.in = in
	return &admindomain.Bucket{}, nil
}

func TestCreateBucket_ForwardsTheOwnerTenant(t *testing.T) {
	owner := uuid.New()
	h := &recordingBucketCreate{}
	srv := &BucketServer{H: h}

	_, err := srv.CreateBucket(context.Background(), connect.NewRequest(&pb.CreateBucketRequest{
		Parent: "storageBackends/primary", BucketId: "b1",
		Bucket: &pb.Bucket{OwnerTenantId: owner.String()},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.in.Bucket.OwnerTenantID != owner {
		t.Errorf("owner_tenant_id did not reach the handler (%v) — the bucket is "+
			"created unowned and the response reports the owner that was asked for",
			h.in.Bucket.OwnerTenantID)
	}
}

type recordingTenant struct {
	failingTenant
	args tenant.CreateTenantArgs
}

func (r *recordingTenant) CreateTenant(_ context.Context, args tenant.CreateTenantArgs) (*tenant.Tenant, error) {
	r.args = args
	return &tenant.Tenant{}, nil
}

// Labels are marshalled only when there are some. Marshalling an empty map
// instead writes the JSON object "{}" where the column should hold NULL —
// no error, and every later read has to treat the two as the same thing.
func TestCreateTenant_LeavesAbsentLabelsUnset(t *testing.T) {
	t.Run("no labels", func(t *testing.T) {
		h := &recordingTenant{}
		srv := &TenantServer{H: h}
		_, err := srv.CreateTenant(context.Background(), connect.NewRequest(&pb.CreateTenantRequest{
			TenantId: uuid.NewString(), Tenant: &pb.Tenant{DisplayName: "Acme"},
		}))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if h.args.Labels != nil {
			t.Errorf("labels marshalled to %q for a request that carried none", h.args.Labels)
		}
	})

	t.Run("with labels", func(t *testing.T) {
		h := &recordingTenant{}
		srv := &TenantServer{H: h}
		_, err := srv.CreateTenant(context.Background(), connect.NewRequest(&pb.CreateTenantRequest{
			TenantId: uuid.NewString(),
			Tenant:   &pb.Tenant{DisplayName: "Acme", Labels: map[string]string{"tier": "gold"}},
		}))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if !bytes.Contains(h.args.Labels, []byte("gold")) {
			t.Errorf("labels did not reach the handler: %q", h.args.Labels)
		}
	})
}

// parseQuotaName's bucket case checks the shape AND the three fixed segments.
// Loosening the conjunction lets any five-segment name through as a bucket
// quota, so "a/b/c/d/e" would be answered with a quota for backend "b",
// bucket "d" — a number for a bucket nobody named.
func TestGetQuota_RejectsAFiveSegmentNameThatIsNotABucketQuota(t *testing.T) {
	srv := &QuotaServer{H: &recordingQuota{}}
	_, err := srv.GetQuota(context.Background(), connect.NewRequest(&pb.GetQuotaRequest{
		Name: "a/b/c/d/e",
	}))
	if err == nil {
		t.Fatal("a name that is not a quota name was accepted")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// unlimited_only and threshold_pct are mutually exclusive only when the
// threshold is non-zero — zero is the field's absent value, and rejecting it
// would refuse the plain "show me the unlimited tenants" query.
func TestSummarize_UnlimitedOnlyWithNoThresholdIsAllowed(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Summarize(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSummarizeRequest{
		UnlimitedOnly: true, // ThresholdPct left at zero: absent, not "0%"
	}))
	if err != nil {
		t.Fatalf("unlimited_only with no threshold was refused: %v", err)
	}
}

func TestSummarize_RejectsUnlimitedOnlyWithAThreshold(t *testing.T) {
	srv := NewTenantBudgetServer(&fakeUsageStore{})
	_, err := srv.Summarize(context.Background(), connect.NewRequest(&pb.TenantBudgetServiceSummarizeRequest{
		UnlimitedOnly: true, ThresholdPct: 80,
	}))
	if err == nil {
		t.Fatal("two mutually exclusive filters were accepted together")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// Subscriptions are RLS-isolated, so a listing that does not narrow to the
// tenant in the parent is a listing scoped by whatever the handler defaults
// to. Inverting this check survived an eighty-mutation sample and had to be
// hit deliberately — a reminder that a sampled score is not a proof about any
// particular line.
type recordingSubs struct {
	failingEventSubscription
	args admindomain.ListEventSubscriptionsArgs
}

func (r *recordingSubs) List(_ context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	r.args = args
	return nil, "", nil
}

func TestListSubscriptions_NarrowsToTheParentTenant(t *testing.T) {
	tenantID := uuid.New()
	h := &recordingSubs{}
	srv := &EventSubscriptionServer{H: h, Tenants: failingTenant{}}

	_, err := srv.ListSubscriptions(context.Background(), connect.NewRequest(&pb.ListSubscriptionsRequest{
		Parent: "tenants/" + tenantID.String(),
	}))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.args.TenantID != tenantID {
		t.Errorf("the listing was scoped to %v, not the tenant the caller named — "+
			"a request for one tenant's subscriptions answered from another's",
			h.args.TenantID)
	}
}
