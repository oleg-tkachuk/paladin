package quotah

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

// allowAuthorizer / denyAuthorizer let the tests isolate the role guard (which
// fires first) from the Cedar guard (which fires second).
type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// fakeQuotaRepo implements quotah.Repository (admindomain.QuotaRepository + the
// ADR-0003 tx seam). RunInTx invokes fn with a nil tx; the *Tx methods ignore
// it and just record that they ran.
type fakeQuotaRepo struct {
	tenantQuota  admindomain.Quota
	tenantErr    error
	bucketQuota  admindomain.Quota
	upsertTenant int
	upsertBucket int
	resetCalls   int
	bucketResets int
	resetErr     error
	// byID is what GetByID returns; byIDErr overrides it. byIDCtx and
	// resetCtx keep the contexts each call ran under.
	byID     admindomain.Quota
	byIDErr  error
	byIDCtx  context.Context
	resetCtx context.Context
}

func (f *fakeQuotaRepo) UpsertTenant(context.Context, admindomain.Quota) error { return nil }
func (f *fakeQuotaRepo) UpsertBucket(context.Context, admindomain.Quota) error { return nil }
func (f *fakeQuotaRepo) GetTenant(context.Context, uuid.UUID) (admindomain.Quota, error) {
	return f.tenantQuota, f.tenantErr
}
func (f *fakeQuotaRepo) GetBucket(context.Context, string, string) (admindomain.Quota, error) {
	return f.bucketQuota, nil
}
func (f *fakeQuotaRepo) IncrementUsage(context.Context, uuid.UUID, int64, int64) error { return nil }
func (f *fakeQuotaRepo) ResetDaily(ctx context.Context, _ uuid.UUID, _ time.Time) error {
	f.resetCalls++
	f.resetCtx = ctx
	return f.resetErr
}
func (f *fakeQuotaRepo) ResetBucketDaily(context.Context, uuid.UUID, time.Time) error {
	f.bucketResets++
	return f.resetErr
}
func (f *fakeQuotaRepo) GetByID(ctx context.Context, _ uuid.UUID) (admindomain.Quota, error) {
	f.byIDCtx = ctx
	return f.byID, f.byIDErr
}

func (f *fakeQuotaRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}
func (f *fakeQuotaRepo) UpsertTenantTx(context.Context, pgx.Tx, admindomain.Quota) error {
	f.upsertTenant++
	return nil
}
func (f *fakeQuotaRepo) UpsertBucketTx(context.Context, pgx.Tx, admindomain.Quota) error {
	f.upsertBucket++
	return nil
}
func (f *fakeQuotaRepo) GetBucketTx(context.Context, pgx.Tx, string, string) (admindomain.Quota, error) {
	return f.bucketQuota, nil
}

func ctxAs(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  "tester",
		TenantID: tenant,
		Roles:    roles,
	})
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

// recordingAuthorizer allows everything and keeps the resource it was asked
// about, so a test can see what Cedar was shown.
type recordingAuthorizer struct {
	decision cedar.Decision
	resource *cedar.Resource
}

func (r *recordingAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, res *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	r.resource = res
	return r.decision, nil
}

// ─── GetTenantQuota ──────────────────────────────────────────────────────────

func TestGetTenantQuota_CrossTenantDenied(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()
	h := NewHandler(&fakeQuotaRepo{}, allowAuthorizer{})
	_, err := h.GetTenantQuota(ctxAs(caller, apiutil.RoleTenantAdmin), other)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestGetTenantQuota_SameTenantAllowed(t *testing.T) {
	caller := uuid.New()
	repo := &fakeQuotaRepo{tenantQuota: admindomain.Quota{TenantID: caller, MaxTotalBytes: 42}}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.GetTenantQuota(ctxAs(caller, apiutil.RoleTenantAdmin), caller)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTotalBytes != 42 {
		t.Errorf("got %d, want the repo quota (42)", got.MaxTotalBytes)
	}
}

func TestGetTenantQuota_PlatformAdminCrossTenant(t *testing.T) {
	repo := &fakeQuotaRepo{tenantQuota: admindomain.Quota{}}
	h := NewHandler(repo, allowAuthorizer{})
	// platform.admin may read any tenant's quota.
	if _, err := h.GetTenantQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New()); err != nil {
		t.Fatalf("platform admin cross-tenant read should pass, got %v", err)
	}
}

func TestGetTenantQuota_NotFoundMapped(t *testing.T) {
	caller := uuid.New()
	repo := &fakeQuotaRepo{tenantErr: admindomain.ErrNotFound}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.GetTenantQuota(ctxAs(caller, apiutil.RoleTenantAdmin), caller)
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}

func TestGetTenantQuota_CedarDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeQuotaRepo{}, denyAuthorizer{})
	_, err := h.GetTenantQuota(ctxAs(caller, apiutil.RoleTenantAdmin), caller)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar deny)", codeOf(err))
	}
}

// ─── GetBucketQuota ──────────────────────────────────────────────────────────

func TestGetBucketQuota_RoleGate(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{}, allowAuthorizer{})
	// tenant.user is not in {platform, bucket, tenant}.admin.
	_, err := h.GetBucketQuota(ctxAs(uuid.New(), apiutil.RoleTenantUser), "be", "bk")
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestGetBucketQuota_Allowed(t *testing.T) {
	repo := &fakeQuotaRepo{bucketQuota: admindomain.Quota{BackendID: "be", BucketName: "bk"}}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.GetBucketQuota(ctxAs(uuid.New(), apiutil.RoleBucketAdmin), "be", "bk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BucketName != "bk" {
		t.Errorf("got %q, want bk", got.BucketName)
	}
}

// ─── SetQuota ────────────────────────────────────────────────────────────────

func TestSetQuota_RoleGate(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{}, allowAuthorizer{})
	_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RoleTenantUser),
		admindomain.Quota{TenantID: uuid.New()}, nil)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestSetQuota_ScopeValidation(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{}, allowAuthorizer{})
	ctx := ctxAs(uuid.New(), apiutil.RolePlatformAdmin)

	// Both scopes set → invalid.
	_, err := h.SetQuota(ctx, admindomain.Quota{
		TenantID: uuid.New(), BackendID: "be", BucketName: "bk",
	}, nil)
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("both-scopes: code = %v, want InvalidArgument", codeOf(err))
	}

	// Neither scope set → invalid.
	_, err = h.SetQuota(ctx, admindomain.Quota{}, nil)
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no-scope: code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestSetQuota_TenantScopeUpsertsInTx(t *testing.T) {
	tid := uuid.New()
	repo := &fakeQuotaRepo{tenantQuota: admindomain.Quota{TenantID: tid, MaxTotalBytes: 7}}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{TenantID: tid, MaxTotalBytes: 7}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.upsertTenant != 1 {
		t.Errorf("UpsertTenantTx called %d times, want 1", repo.upsertTenant)
	}
	if repo.upsertBucket != 0 {
		t.Errorf("UpsertBucketTx should not run for tenant scope, ran %d", repo.upsertBucket)
	}
	if got.MaxTotalBytes != 7 {
		t.Errorf("response = %d, want the read-back quota (7)", got.MaxTotalBytes)
	}
}

func TestSetQuota_BucketScopeUpsertsInTx(t *testing.T) {
	repo := &fakeQuotaRepo{bucketQuota: admindomain.Quota{BackendID: "be", BucketName: "bk"}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RoleBucketAdmin),
		admindomain.Quota{BackendID: "be", BucketName: "bk"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.upsertBucket != 1 || repo.upsertTenant != 0 {
		t.Errorf("bucket scope: upsertBucket=%d upsertTenant=%d, want 1/0",
			repo.upsertBucket, repo.upsertTenant)
	}
}

func TestSetQuota_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{}, denyAuthorizer{})
	_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{TenantID: uuid.New()}, nil)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar deny)", codeOf(err))
	}
}

// recordingEvents keeps the tenant each event was fanned out to.
type recordingEvents struct{ tenants []string }

func (r *recordingEvents) Dispatch(_ context.Context, tenantID string, _ worker.Event) (int, error) {
	r.tenants = append(r.tenants, tenantID)
	return 1, nil
}

func (r *recordingEvents) DispatchTx(_ context.Context, _ pgx.Tx, tenantID string, _ worker.Event) (int, error) {
	r.tenants = append(r.tenants, tenantID)
	return 1, nil
}

// A bucket quota's paladin.quota.set goes to the tenant owning the bucket.
func TestSetQuota_BucketScopeNotifiesTheBucketOwner(t *testing.T) {
	owner := uuid.New()
	repo := &fakeQuotaRepo{bucketQuota: admindomain.Quota{
		BackendID: "primary", BucketName: "dedicated", OwnerTenantID: owner,
	}}
	events := &recordingEvents{}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetEventProducer(events)
	if _, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{BackendID: "primary", BucketName: "dedicated"}, nil); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if len(events.tenants) != 1 || events.tenants[0] != owner.String() {
		t.Errorf("events went to %v, want [%s]", events.tenants, owner)
	}
}

// A shared bucket has no owner, so its quota change notifies nobody.
func TestSetQuota_SharedBucketNotifiesNobody(t *testing.T) {
	repo := &fakeQuotaRepo{bucketQuota: admindomain.Quota{BackendID: "primary", BucketName: "shared"}}
	events := &recordingEvents{}
	h := NewHandler(repo, allowAuthorizer{})
	h.SetEventProducer(events)
	if _, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{BackendID: "primary", BucketName: "shared"}, nil); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if len(events.tenants) != 0 {
		t.Errorf("events went to %v, want none", events.tenants)
	}
}

// ─── ResetUsage ──────────────────────────────────────────────────────────────

func TestResetUsage_RequiresPlatformAdmin(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{}, allowAuthorizer{})
	// bucket.admin is insufficient — ResetUsage is platform-only.
	err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RoleBucketAdmin), uuid.New())
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestResetUsage_PlatformAdminResets(t *testing.T) {
	owner := uuid.New()
	repo := &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), TenantID: owner}}
	h := NewHandler(repo, allowAuthorizer{})
	if err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.resetCalls != 1 {
		t.Fatalf("ResetDaily calls = %d, want 1", repo.resetCalls)
	}
}

// The quota is loaded before Cedar runs, so a policy sees which tenant's
// quota is being reset rather than an empty entity.
func TestResetUsage_CedarSeesTheQuotasTenant(t *testing.T) {
	owner := uuid.New()
	repo := &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), TenantID: owner}}
	authz := &recordingAuthorizer{decision: cedar.DecisionAllow}
	h := NewHandler(repo, authz)
	if err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authz.resource == nil || authz.resource.TenantID != owner {
		t.Fatalf("Cedar resource = %+v, want TenantID %s", authz.resource, owner)
	}
}

// The quota belongs to another tenant, so it is read cross-tenant and reset
// acting as its owner — under RLS the caller's own scope matches no row.
func TestResetUsage_ReadsCrossTenantAndResetsAsTheOwner(t *testing.T) {
	owner := uuid.New()
	repo := &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), TenantID: owner}}
	h := NewHandler(repo, allowAuthorizer{})
	if err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auth.CrossTenantRead(repo.byIDCtx) {
		t.Error("GetByID ran without the cross-tenant read flag")
	}
	if got, ok := auth.ActingTenant(repo.resetCtx); !ok || got != owner {
		t.Errorf("ResetDaily acting tenant = (%s, %v), want %s", got, ok, owner)
	}
	if auth.CrossTenantRead(repo.resetCtx) {
		t.Error("ResetDaily ran with the cross-tenant read flag; the write must be tenant-scoped")
	}
}

func TestResetUsage_CedarDeniedResetsNothing(t *testing.T) {
	repo := &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), TenantID: uuid.New()}}
	h := NewHandler(repo, denyAuthorizer{})
	err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
	if repo.resetCalls != 0 {
		t.Errorf("ResetDaily calls = %d after a deny, want 0", repo.resetCalls)
	}
}

func TestResetUsage_UnknownQuotaIsNotFound(t *testing.T) {
	repo := &fakeQuotaRepo{byIDErr: admindomain.ErrNotFound}
	h := NewHandler(repo, allowAuthorizer{})
	err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
	if repo.resetCalls != 0 {
		t.Errorf("ResetDaily calls = %d, want 0", repo.resetCalls)
	}
}

// A reset that matched no row reports it rather than succeeding silently.
func TestResetUsage_ResetMissingRowIsNotFound(t *testing.T) {
	repo := &fakeQuotaRepo{
		byID:     admindomain.Quota{QuotaID: uuid.New(), TenantID: uuid.New()},
		resetErr: admindomain.ErrNotFound,
	}
	h := NewHandler(repo, allowAuthorizer{})
	err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID)
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}

// A bucket quota resets through the bucket table, and Cedar sees the bucket.
func TestResetUsage_BucketScopedQuotaResetsTheBucketRow(t *testing.T) {
	repo := &fakeQuotaRepo{byID: admindomain.Quota{
		QuotaID: uuid.New(), BackendID: "primary", BucketName: "shared",
	}}
	authz := &recordingAuthorizer{decision: cedar.DecisionAllow}
	h := NewHandler(repo, authz)
	if err := h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), repo.byID.QuotaID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.bucketResets != 1 || repo.resetCalls != 0 {
		t.Errorf("bucket resets = %d, tenant resets = %d; want 1 and 0",
			repo.bucketResets, repo.resetCalls)
	}
	if authz.resource == nil || authz.resource.BucketName != "shared" || authz.resource.BackendID != "primary" {
		t.Errorf("Cedar resource = %+v, want backend primary, bucket shared", authz.resource)
	}
}

// Compile-time assertions that the doubles satisfy the handler's interfaces.
var (
	_ Repository       = (*fakeQuotaRepo)(nil)
	_ cedar.Authorizer = allowAuthorizer{}
	_ cedar.Authorizer = denyAuthorizer{}
)
