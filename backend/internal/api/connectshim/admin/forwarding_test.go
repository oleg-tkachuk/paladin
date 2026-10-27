package admin

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	policyh "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/policyh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
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

	_, err := srv.ListBuckets(context.Background(), &pb.ListBucketsRequest{
		Parent:        "storageBackends/primary",
		OwnerTenantId: owner.String(),
		Page:          &commonv1.PageRequest{PageToken: "primary/b1"},
	})
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

func TestListBuckets_ForwardsTheUsedByTenantFilter(t *testing.T) {
	tenant := uuid.New()
	h := &recordingBuckets{}
	srv := &BucketServer{H: h}

	_, err := srv.ListBuckets(context.Background(), &pb.ListBucketsRequest{
		UsedByTenantId: tenant.String(),
	})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.listArgs.UsedByTenantID == nil || *h.listArgs.UsedByTenantID != tenant {
		t.Errorf("used_by_tenant_id did not reach the handler (got %v) — a tenant's "+
			"bucket list would show every tenant's buckets", h.listArgs.UsedByTenantID)
	}
	if h.listArgs.OwnerTenantID != nil {
		t.Errorf("used_by_tenant_id leaked into the ownership filter (%v) — a "+
			"shared-layout tenant owns nothing and would list nothing",
			h.listArgs.OwnerTenantID)
	}
}

func TestListBuckets_RefusesAUsedByTenantThatIsNotAUUID(t *testing.T) {
	h := &recordingBuckets{}
	srv := &BucketServer{H: h}

	_, err := srv.ListBuckets(context.Background(), &pb.ListBucketsRequest{
		UsedByTenantId: "acme",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a slug for used_by_tenant_id: got %v, want InvalidArgument — "+
			"dropping it silently lists every tenant's buckets", err)
	}
}

func TestUpdateBucket_ForwardsTheOwnerTenant(t *testing.T) {
	owner := uuid.New()
	h := &recordingBuckets{}
	srv := &BucketServer{H: h}

	_, err := srv.UpdateBucket(context.Background(), &pb.UpdateBucketRequest{
		Name: "storageBackends/primary/buckets/b1", ResourceVersion: "1",
		Bucket: &pb.Bucket{OwnerTenantId: owner.String()},
	})
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

	_, err := srv.ListCollections(context.Background(), &pb.ListCollectionsRequest{
		Parent: "tenants/" + tenantID.String(),
		Bucket: "storageBackends/primary/buckets/b1",
	})
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

	_, err := srv.CreateCollection(context.Background(), &pb.CreateCollectionRequest{
		Parent: "tenants/" + uuid.NewString(), Collection: "c1",
		CollectionResource: &pb.Collection{Bucket: "storageBackends/other/buckets/b2"},
	})
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

	_, err := srv.SimulateAuthz(context.Background(), &pb.SimulateAuthzRequest{
		ResourceName: "tenants/" + tenantID.String() + "/collections/c1",
		Action:       "read", PrincipalSubject: "tester",
		PrincipalTenantId: tenantID.String(),
	})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if h.in.PrincipalTenantID != tenantID {
		t.Errorf("principal_tenant_id did not reach the handler (%v) — the "+
			"simulation answers for a principal in no tenant, which is not the "+
			"question that was asked", h.in.PrincipalTenantID)
	}
}

type validatingPolicy struct {
	failingPolicy
	out *policyh.ValidateOutput
}

func (v validatingPolicy) ValidatePolicy(context.Context, string) (*policyh.ValidateOutput, error) {
	return v.out, nil
}

// A policy that compiles but does not type-check is ok with warnings; the
// console blocks saving on errors only, so the severity has to survive.
func TestValidate_ForwardsEveryDiagnosticWithItsSeverity(t *testing.T) {
	srv := &PolicyServer{H: validatingPolicy{out: &policyh.ValidateOutput{
		OK: true,
		Diagnostics: []policyh.Diagnostic{
			{Severity: policyh.DiagnosticWarning, Message: "first"},
			{Severity: policyh.DiagnosticWarning, Message: "second"},
		},
	}}}
	res, err := srv.Validate(context.Background(), &pb.ValidateRequest{CedarPolicy: "x"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !res.GetOk() || len(res.GetDiagnostics()) != 2 {
		t.Fatalf("got ok=%v diagnostics=%v, want ok and both warnings", res.GetOk(), res.GetDiagnostics())
	}
	for _, d := range res.GetDiagnostics() {
		if d.GetSeverity() != policyh.DiagnosticWarning {
			t.Errorf("severity = %q, want %q", d.GetSeverity(), policyh.DiagnosticWarning)
		}
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
		_, err := srv.GetQuota(context.Background(), &pb.GetQuotaRequest{
			Name: "storageBackends/primary/buckets/b1/quota",
		})
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
		_, err := srv.GetQuota(context.Background(), &pb.GetQuotaRequest{
			Name: "tenants/" + tenantID.String() + "/quota",
		})
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
	if got := tenantDomainToProto(nil, 0); got != nil {
		t.Errorf("tenantDomainToProto(nil, 0) = %v, want nil", got)
	}
}

// A cursor that decodes to the zero value is a cursor that pages forever from
// the start. decodeAuditCursor answers zero for anything it cannot read, so
// the difference between "unreadable" and "read correctly" is invisible unless
// something checks what came out — inverting either parse check survived.
type recordingAudit struct {
	failingAudit
	args    admindomain.ListAuditArgs
	counted *admindomain.ListAuditArgs
}

func (r *recordingAudit) CountAuditFacets(_ context.Context, args admindomain.ListAuditArgs, _ string) ([]admindomain.AuditFacetCount, error) {
	r.counted = &args
	return []admindomain.AuditFacetCount{
		{Facet: admindomain.AuditFacetOutcome, Value: string(admindomain.AuditOutcomeError), Count: 2},
		{Facet: admindomain.AuditFacetPlane, Value: auditPlaneAdmin, Count: 3},
	}, nil
}

// auditPlaneAdmin is an actor_audience the audit facet tests narrow by.
const auditPlaneAdmin = "paladin-admin"

// The facets narrow the listing as the domain names them, and are counted
// only when asked and only with the first page; an outcome is counted under
// its AuditOutcome name.
func TestListAuditLog_FacetCounts(t *testing.T) {
	facets := &pb.AuditFacets{
		Planes:   []string{auditPlaneAdmin},
		Outcomes: []pb.AuditOutcome{pb.AuditOutcome_AUDIT_OUTCOME_ERROR, pb.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED},
	}
	for _, tc := range []struct {
		name    string
		count   bool
		token   string
		counted bool
	}{
		{"asked, first page", true, "", true},
		{"asked, a later page", true, "tok", false},
		{"not asked", false, "", false},
	} {
		h := &recordingAudit{}
		out, err := (&AuditServer{H: h}).ListAuditLog(context.Background(), &pb.ListAuditLogRequest{
			Facets: facets, CountFacets: tc.count, Page: &commonv1.PageRequest{PageToken: tc.token},
		})
		if err != nil {
			t.Fatal(err)
		}
		f := h.args.Facets
		if len(f.Planes) != 1 || len(f.Outcomes) != 1 || f.Outcomes[0] != admindomain.AuditOutcomeError {
			t.Errorf("%s: listed with %+v; want the plane and the one known outcome", tc.name, f)
		}
		if (h.counted != nil) != tc.counted || (len(out.GetFacetCounts()) != 0) != tc.counted {
			t.Errorf("%s: counted %t with %d counts, want %t", tc.name, h.counted != nil, len(out.GetFacetCounts()), tc.counted)
		}
		if tc.counted && out.GetFacetCounts()[0].GetValue() != pb.AuditOutcome_AUDIT_OUTCOME_ERROR.String() {
			t.Errorf("%s: outcome counted as %q", tc.name, out.GetFacetCounts()[0].GetValue())
		}
	}
}

func (r *recordingAudit) ListAuditLog(_ context.Context, args admindomain.ListAuditArgs, _ string) ([]admindomain.AuditEntry, string, error) {
	r.args = args
	return nil, "", nil
}

// The page token reaches the handler as sent, for the store to read against
// the order the request names.
func TestListAuditLog_ForwardsTheTokenAndOrder(t *testing.T) {
	cases := []struct {
		name    string
		orderBy string
		dir     commonv1.SortOrder
		want    keyset.Order
	}{
		{"no order is the handler's default", "", commonv1.SortOrder_SORT_ORDER_UNSPECIFIED, keyset.Order{}},
		{"no order descending is the same", "", commonv1.SortOrder_SORT_ORDER_DESC, keyset.Order{}},
		{"no order ascending turns it around", "", commonv1.SortOrder_SORT_ORDER_ASC, keyset.Order{Field: admindomain.AuditOrderByAt}},
		{"a named field is ascending by default", admindomain.AuditOrderByAction, commonv1.SortOrder_SORT_ORDER_UNSPECIFIED, keyset.Order{Field: admindomain.AuditOrderByAction}},
		{"and descending when asked", admindomain.AuditOrderByAction, commonv1.SortOrder_SORT_ORDER_DESC, keyset.Order{Field: admindomain.AuditOrderByAction, Desc: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &recordingAudit{}
			srv := &AuditServer{H: h}
			if _, err := srv.ListAuditLog(context.Background(), &pb.ListAuditLogRequest{
				Page:      &commonv1.PageRequest{PageToken: "tok"},
				OrderBy:   tc.orderBy,
				SortOrder: tc.dir,
			}); err != nil {
				t.Fatalf("listing: %v", err)
			}
			if h.args.PageToken != "tok" || h.args.Order != tc.want {
				t.Errorf("handler got token %q, order %+v; want tok, %+v", h.args.PageToken, h.args.Order, tc.want)
			}
		})
	}
}

// The tenant a request names reaches the handler as the trail to read; a
// value that is not a UUID is refused rather than dropped, which would list
// every tenant's entries.
func TestListAuditLog_NamesTheTrailTenant(t *testing.T) {
	tenant := uuid.New()
	h := &recordingAudit{}
	srv := &AuditServer{H: h}

	if _, err := srv.ListAuditLog(context.Background(), &pb.ListAuditLogRequest{
		TenantId: tenant.String(),
	}); err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.args.TrailTenantID != tenant {
		t.Errorf("trail = %s, want %s", h.args.TrailTenantID, tenant)
	}

	_, err := srv.ListAuditLog(context.Background(), &pb.ListAuditLogRequest{
		TenantId: "platform",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a tenant that is not a UUID: err = %v, want InvalidArgument", err)
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

	_, err := srv.CreateBucket(context.Background(), &pb.CreateBucketRequest{
		Parent: "storageBackends/primary", BucketId: "b1",
		Bucket: &pb.Bucket{OwnerTenantId: owner.String()},
	})
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
	args tenanth.CreateTenantArgs
}

func (r *recordingTenant) CreateTenant(_ context.Context, args tenanth.CreateTenantArgs) (*tenanth.Tenant, error) {
	r.args = args
	return &tenanth.Tenant{}, nil
}

// Labels are marshalled only when there are some. Marshalling an empty map
// instead writes the JSON object "{}" where the column should hold NULL —
// no error, and every later read has to treat the two as the same thing.
func TestCreateTenant_LeavesAbsentLabelsUnset(t *testing.T) {
	t.Run("no labels", func(t *testing.T) {
		h := &recordingTenant{}
		srv := &TenantServer{H: h}
		_, err := srv.CreateTenant(context.Background(), &pb.CreateTenantRequest{
			TenantId: uuid.NewString(), Tenant: &pb.Tenant{DisplayName: "Acme"},
		})
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
		_, err := srv.CreateTenant(context.Background(), &pb.CreateTenantRequest{
			TenantId: uuid.NewString(),
			Tenant:   &pb.Tenant{DisplayName: "Acme", Labels: map[string]string{"tier": "gold"}},
		})
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
	_, err := srv.GetQuota(context.Background(), &pb.GetQuotaRequest{
		Name: "a/b/c/d/e",
	})
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
	srv := newServer(&fakeUsageStore{})
	_, err := srv.Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		UnlimitedOnly: true, // ThresholdPct left at zero: absent, not "0%"
	})
	if err != nil {
		t.Fatalf("unlimited_only with no threshold was refused: %v", err)
	}
}

func TestSummarize_RejectsUnlimitedOnlyWithAThreshold(t *testing.T) {
	srv := newServer(&fakeUsageStore{})
	_, err := srv.Summarize(context.Background(), &pb.TenantBudgetServiceSummarizeRequest{
		UnlimitedOnly: true, ThresholdPct: 80,
	})
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

	_, err := srv.ListSubscriptions(context.Background(), &pb.ListSubscriptionsRequest{
		Parent: "tenants/" + tenantID.String(),
	})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if h.args.TenantID != tenantID {
		t.Errorf("the listing was scoped to %v, not the tenant the caller named — "+
			"a request for one tenant's subscriptions answered from another's",
			h.args.TenantID)
	}
}

// deniedPolicy refuses every inspection, as the handler's Cedar gate does.
type deniedPolicy struct{ failingPolicy }

func (deniedPolicy) GetEffectivePolicy(context.Context, string, uuid.UUID) (*policyh.EffectivePolicyOutput, error) {
	return nil, connect.NewError(connect.CodePermissionDenied, "denied by policy")
}

// The handler's code reaches the caller. Every error used to be wrapped as
// Internal, so a caller refused by policy read it as a server fault.
func TestGetEffectivePolicy_KeepsTheHandlersCode(t *testing.T) {
	srv := &PolicyServer{H: deniedPolicy{}}
	_, err := srv.GetEffectivePolicy(context.Background(), &pb.GetEffectivePolicyRequest{
		ResourceName: "tenants/" + uuid.NewString(),
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v (%v), want PermissionDenied", connect.CodeOf(err), err)
	}
}

// owner_tenant_id names the tenant by UUID. A slug used to be parsed, fail,
// and be dropped: the call succeeded and the bucket was shared, while the
// caller believed it owned.
func TestBucketOwnerTenant_RefusesANonUUID(t *testing.T) {
	const slug = "smoke-t21941"
	t.Run("create", func(t *testing.T) {
		srv := &BucketServer{H: &recordingBucketCreate{}}
		_, err := srv.CreateBucket(context.Background(), &pb.CreateBucketRequest{
			Parent: "storageBackends/primary", BucketId: "b1",
			Bucket: &pb.Bucket{OwnerTenantId: slug},
		})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("owner by slug: err = %v, want InvalidArgument", err)
		}
	})
	t.Run("update", func(t *testing.T) {
		srv := &BucketServer{H: &recordingBuckets{}}
		_, err := srv.UpdateBucket(context.Background(), &pb.UpdateBucketRequest{
			Name: "storageBackends/primary/buckets/b1", ResourceVersion: "1",
			Bucket: &pb.Bucket{OwnerTenantId: slug},
		})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("owner by slug: err = %v, want InvalidArgument", err)
		}
	})
	t.Run("list", func(t *testing.T) {
		srv := &BucketServer{H: &recordingBuckets{}}
		_, err := srv.ListBuckets(context.Background(), &pb.ListBucketsRequest{OwnerTenantId: slug})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("owner by slug: err = %v, want InvalidArgument", err)
		}
	})
}

// An empty owner is the shared bucket, not an error: create without one, and
// an update that clears it through the mask.
func TestBucketOwnerTenant_EmptyIsShared(t *testing.T) {
	create := &recordingBucketCreate{}
	if _, err := (&BucketServer{H: create}).CreateBucket(context.Background(), &pb.CreateBucketRequest{
		Parent: "storageBackends/primary", BucketId: "b1", Bucket: &pb.Bucket{},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if create.in.Bucket.OwnerTenantID != uuid.Nil {
		t.Errorf("create without an owner: owner = %v, want none", create.in.Bucket.OwnerTenantID)
	}

	update := &recordingBuckets{}
	if _, err := (&BucketServer{H: update}).UpdateBucket(context.Background(), &pb.UpdateBucketRequest{
		Name: "storageBackends/primary/buckets/b1", ResourceVersion: "1",
		Bucket:     &pb.Bucket{},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"owner_tenant_id"}},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if update.updateArgs.Bucket.OwnerTenantID != uuid.Nil {
		t.Errorf("update clearing the owner: owner = %v, want none", update.updateArgs.Bucket.OwnerTenantID)
	}
}

type versionedPolicy struct {
	failingPolicy
	page    policyh.VersionsPage
	gotName string
	gotSize int32
	gotTok  string
}

func (v *versionedPolicy) ListPolicyVersions(_ context.Context, name string, size int32, token string) (policyh.VersionsPage, error) {
	v.gotName, v.gotSize, v.gotTok = name, size, token
	return v.page, nil
}

// Each version reaches the wire as the handler read it, an unrecorded writer
// staying absent rather than becoming "".
func TestListPolicyVersions_ForwardsEachVersion(t *testing.T) {
	writer := "01a00000-0000-7000-8000-00000000beef"
	writerTenant := uuid.MustParse("01a00000-0000-7000-8000-0000000000aa")
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	const size, token, next = 2, "5", "3"
	h := &versionedPolicy{page: policyh.VersionsPage{
		Versions: []policyh.Version{
			{Version: 4, CedarPolicy: "permit (principal, action, resource);", WrittenBy: &writer, WrittenByTenant: &writerTenant, WrittenAt: at},
			{Version: 3, CedarPolicy: ""},
		},
		NextPageToken: next,
	}}
	resp, err := (&PolicyServer{H: h}).ListPolicyVersions(context.Background(), &pb.ListPolicyVersionsRequest{
		ResourceName: "tenants/t1", Page: &commonv1.PageRequest{PageSize: size, PageToken: token},
	})
	if err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
	if h.gotName != "tenants/t1" || h.gotSize != size || h.gotTok != token {
		t.Errorf("handler got %q %d %q", h.gotName, h.gotSize, h.gotTok)
	}
	v := resp.GetVersions()
	if len(v) != 2 || v[0].GetVersion() != 4 || v[0].GetWrittenBy() != writer || !v[0].GetWrittenAt().AsTime().Equal(at) {
		t.Fatalf("versions = %v", v)
	}
	if v[0].GetWrittenByTenantId() != writerTenant.String() {
		t.Errorf("writer tenant = %q, want %s", v[0].GetWrittenByTenantId(), writerTenant)
	}
	if v[1].WrittenByTenantId != nil {
		t.Errorf("an unrecorded writer's tenant reached the wire as %v", v[1].WrittenByTenantId)
	}
	if v[1].WrittenBy != nil || v[1].GetCedarPolicy() != "" {
		t.Errorf("an unrecorded writer reached the wire as %v", v[1].WrittenBy)
	}
	if resp.GetPage().GetNextPageToken() != next {
		t.Errorf("next page token = %q, want %q", resp.GetPage().GetNextPageToken(), next)
	}
}
