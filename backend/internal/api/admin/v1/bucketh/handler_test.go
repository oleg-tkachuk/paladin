package bucketh

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// fakeRepo implements bucketh.Repository (admindomain.BucketRepository + the
// ADR-0003 tx seam). Only the fields the tests touch carry behavior; the rest
// are no-ops so the interface is satisfied.
type fakeRepo struct {
	backendEnabled bool
	backendEnaErr  error
	getBucket      admindomain.Bucket
	getErr         error
	getTxBucket    admindomain.Bucket
	createTxErr    error
	createTxBucket admindomain.Bucket
	deleteTxErr    error

	// List / ListAccessible: configurable return + captured args.
	listResult []admindomain.Bucket
	listToken  string
	listErr    error
	listArgs   admindomain.ListBucketsArgs

	listAccResult   []admindomain.Bucket
	listAccToken    string
	listAccErr      error
	listAccTenant   uuid.UUID
	listAccPageSize int32
	listAccAfterBk  string
	listAccAfterNm  string

	// Single-purpose setters: configurable err + captured forwarded args.
	setPolicyErr      error
	gotPolicyArgs     setterArgs
	gotPolicyValue    string
	setLifecycleErr   error
	gotLifecycleArg   []admindomain.LifecycleRule
	setLockErr        error
	gotLockArg        admindomain.ObjectLockConfig
	setVersioningErr  error
	gotVersioningArg  admindomain.BucketVersioning
	setReplicationErr error
	gotReplicationArg admindomain.BucketReplication

	// UpdateBasicTx: configurable err + captured args.
	updateBasicTxErr error
	gotUpdateBucket  admindomain.Bucket
	gotUpdateVer     int64
	gotUpdateMask    []string

	// MarkDeletingTx: configurable err + call flags.
	markDeletingTxErr  error
	markDeletingCalled bool
	deleteTxCalled     bool
}

// setterArgs captures the (backend, bucket, expectedVersion) triple the
// single-purpose setters forward to the repo, so tests can assert the
// handler passes them through unchanged.
type setterArgs struct {
	backend string
	bucket  string
	ver     int64
}

// BucketRepository — meaningful methods.
func (f *fakeRepo) Get(context.Context, string, string) (admindomain.Bucket, error) {
	return f.getBucket, f.getErr
}
func (f *fakeRepo) BackendEnabled(context.Context, string) (bool, error) {
	return f.backendEnabled, f.backendEnaErr
}

// BucketRepository — no-op remainder.
func (f *fakeRepo) Create(context.Context, admindomain.Bucket) error { return nil }
func (f *fakeRepo) List(_ context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	f.listArgs = args
	return f.listResult, f.listToken, f.listErr
}
func (f *fakeRepo) ListAccessible(_ context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error) {
	f.listAccTenant = tenantID
	f.listAccPageSize = pageSize
	f.listAccAfterBk = afterBackend
	f.listAccAfterNm = afterName
	return f.listAccResult, f.listAccToken, f.listAccErr
}
func (f *fakeRepo) UpdateBasic(context.Context, admindomain.Bucket, int64, []string) error {
	return nil
}
func (f *fakeRepo) SetPolicy(_ context.Context, backendID, bucketName, policy string, expectedVersion int64) error {
	f.gotPolicyArgs = setterArgs{backendID, bucketName, expectedVersion}
	f.gotPolicyValue = policy
	return f.setPolicyErr
}
func (f *fakeRepo) SetLifecycle(_ context.Context, _, _ string, rules []admindomain.LifecycleRule, _ int64) error {
	f.gotLifecycleArg = rules
	return f.setLifecycleErr
}
func (f *fakeRepo) SetObjectLock(_ context.Context, _, _ string, lock admindomain.ObjectLockConfig, _ int64) error {
	f.gotLockArg = lock
	return f.setLockErr
}
func (f *fakeRepo) SetVersioning(_ context.Context, _, _ string, v admindomain.BucketVersioning, _ int64) error {
	f.gotVersioningArg = v
	return f.setVersioningErr
}
func (f *fakeRepo) SetReplication(_ context.Context, _, _ string, r admindomain.BucketReplication, _ int64) error {
	f.gotReplicationArg = r
	return f.setReplicationErr
}
func (f *fakeRepo) SetConstraints(context.Context, string, string, admindomain.BucketConstraints, int64) error {
	return nil
}
func (f *fakeRepo) Delete(context.Context, string, string, int64) error { return nil }
func (f *fakeRepo) ListPendingProvisions(context.Context, int32, int32) ([]admindomain.BucketProvisionRow, error) {
	return nil, nil
}
func (f *fakeRepo) MarkProvisionReady(context.Context, string, string) error { return nil }
func (f *fakeRepo) MarkProvisionFailed(context.Context, string, string, bool, string) error {
	return nil
}
func (f *fakeRepo) MarkDeleting(context.Context, string, string, int64) error { return nil }
func (f *fakeRepo) ListPendingDeletions(context.Context, int32, int32) ([]admindomain.BucketProvisionRow, error) {
	return nil, nil
}
func (f *fakeRepo) MarkDeletionFailed(context.Context, string, string, bool, string) error {
	return nil
}

// tx seam.
func (f *fakeRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}
func (f *fakeRepo) CreateTx(_ context.Context, _ pgx.Tx, b admindomain.Bucket) error {
	f.createTxBucket = b
	return f.createTxErr
}
func (f *fakeRepo) UpdateBasicTx(_ context.Context, _ pgx.Tx, b admindomain.Bucket, expectedVersion int64, mask []string) error {
	f.gotUpdateBucket = b
	f.gotUpdateVer = expectedVersion
	f.gotUpdateMask = mask
	return f.updateBasicTxErr
}
func (f *fakeRepo) GetTx(context.Context, pgx.Tx, string, string) (admindomain.Bucket, error) {
	return f.getTxBucket, nil
}
func (f *fakeRepo) DeleteTx(context.Context, pgx.Tx, string, string, int64) error {
	f.deleteTxCalled = true
	return f.deleteTxErr
}
func (f *fakeRepo) MarkDeletingTx(context.Context, pgx.Tx, string, string, int64) error {
	f.markDeletingCalled = true
	return f.markDeletingTxErr
}

type okProvisioner struct{}

func (okProvisioner) CreateBucket(context.Context, string, string, string) error { return nil }
func (okProvisioner) DeleteBucket(context.Context, string, string) error         { return nil }

func ctxAs(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: uuid.New(), Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

func validBucket() admindomain.Bucket {
	return admindomain.Bucket{BackendID: "primary", BucketId: "acme-logs", OwnerTenantID: uuid.New()}
}

// ─── CreateBucket ────────────────────────────────────────────────────────────

func TestCreateBucket_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, okProvisioner{}, allowAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleTenantUser), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestCreateBucket_RequiresBackendAndName(t *testing.T) {
	h := NewHandler(&fakeRepo{}, okProvisioner{}, allowAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: admindomain.Bucket{BucketId: "x"}}) // no backend
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestCreateBucket_BackendNotFound(t *testing.T) {
	repo := &fakeRepo{backendEnaErr: admindomain.ErrNotFound}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (unknown backend)", code(err))
	}
}

func TestCreateBucket_BackendDisabled(t *testing.T) {
	repo := &fakeRepo{backendEnabled: false}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (disabled backend)", code(err))
	}
}

func TestCreateBucket_ProvisionWithoutProvisioner(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true}
	h := NewHandler(repo, nil, allowAuthorizer{}) // no provisioner
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
	if code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
}

func TestCreateBucket_NoProvisionMarksReady(t *testing.T) {
	b := validBucket()
	repo := &fakeRepo{backendEnabled: true, getTxBucket: b}
	h := NewHandler(repo, nil, allowAuthorizer{})
	got, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: b, ProvisionOnBackend: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createTxBucket.ProvisionState != admindomain.BucketProvisionStateReady {
		t.Errorf("persisted provision_state = %q, want ready",
			repo.createTxBucket.ProvisionState)
	}
	if got.BucketName != "acme-logs" {
		t.Errorf("returned bucket = %q, want the read-back row", got.BucketName)
	}
}

func TestCreateBucket_ProvisionMarksPending(t *testing.T) {
	b := validBucket()
	repo := &fakeRepo{backendEnabled: true, getTxBucket: b}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	if _, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: b, ProvisionOnBackend: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createTxBucket.ProvisionState != admindomain.BucketProvisionStatePending {
		t.Errorf("persisted provision_state = %q, want pending",
			repo.createTxBucket.ProvisionState)
	}
}

func TestCreateBucket_ConflictMapped(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true, createTxErr: admindomain.ErrConflict}
	h := NewHandler(repo, nil, allowAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (conflict)", code(err))
	}
}

func TestCreateBucket_CedarDenied(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true}
	h := NewHandler(repo, nil, denyAuthorizer{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

// ─── GetBucket ───────────────────────────────────────────────────────────────

func TestGetBucket_NotFound(t *testing.T) {
	repo := &fakeRepo{getErr: admindomain.ErrNotFound}
	h := NewHandler(repo, nil, allowAuthorizer{})
	_, err := h.GetBucket(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme")
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGetBucket_TenantAdminRedacted(t *testing.T) {
	full := admindomain.Bucket{
		BackendID: "primary", BucketId: "acme", OwnerTenantID: uuid.New(),
		CedarPolicy: "permit(...);",
		Replication: admindomain.BucketReplication{DestinationBucket: "dr-bucket"},
	}
	repo := &fakeRepo{getBucket: full}
	h := NewHandler(repo, nil, allowAuthorizer{})
	got, err := h.GetBucket(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CedarPolicy != "" {
		t.Error("tenant admin should not see the cedar_policy text")
	}
	if got.Replication.DestinationBucket != "" {
		t.Error("tenant admin should not see the replication destination")
	}
}

func TestGetBucket_BucketAdminSeesFull(t *testing.T) {
	full := admindomain.Bucket{
		BackendID: "primary", BucketId: "acme", OwnerTenantID: uuid.New(),
		CedarPolicy: "permit(...);",
		Replication: admindomain.BucketReplication{DestinationBucket: "dr-bucket"},
	}
	repo := &fakeRepo{getBucket: full}
	h := NewHandler(repo, nil, allowAuthorizer{})
	got, err := h.GetBucket(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CedarPolicy == "" || got.Replication.DestinationBucket == "" {
		t.Error("bucket admin should see the full record, no redaction")
	}
}

// ─── DeleteBucket ────────────────────────────────────────────────────────────

func TestDeleteBucket_DeleteOnBackendWithoutProvisioner(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketId: "acme", DeleteOnBackend: true})
	if code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
}

func TestDeleteBucket_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{deleteTxErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, nil, allowAuthorizer{})
	// DeleteOnBackend=false → physical row delete path (DeleteTx) under OCC.
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketId: "acme", ExpectedVersion: 1})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// Compile-time interface assertions.
var (
	_ Repository       = (*fakeRepo)(nil)
	_ Provisioner      = okProvisioner{}
	_ cedar.Authorizer = allowAuthorizer{}
)
