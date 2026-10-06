package bucketh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
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

	// CountCollectionsReferencing: configurable count, plus whether the
	// call arrived under a cross-tenant read. The second field is the one
	// that matters — a tenant-scoped count silently returns zero against
	// the real RLS pool, so a guard that forgets the widening reads as
	// "no references" on exactly the buckets it is meant to protect.
	getCalls         int
	getNotFoundFirst bool

	bucketRefs      []admindomain.BucketReference
	bucketRefsErr   error
	bucketRefsCross bool

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
	f.getCalls++
	// EnsureBucket calls Get twice on the race path — once to look before it
	// writes, once after the write loses. getNotFoundFirst makes the first
	// call miss so the create is actually attempted; without it the fast
	// idempotent path returns and the race branch is never reached.
	if f.getNotFoundFirst && f.getCalls == 1 {
		return admindomain.Bucket{}, admindomain.ErrNotFound
	}
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
func (f *fakeRepo) CountBucketReferences(ctx context.Context, _, _ string) ([]admindomain.BucketReference, error) {
	f.bucketRefsCross = auth.CrossTenantRead(ctx)
	return f.bucketRefs, f.bucketRefsErr
}
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
	return admindomain.Bucket{BackendID: "primary", BucketName: "acme-logs", OwnerTenantID: uuid.New()}
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
		CreateBucketInput{Bucket: admindomain.Bucket{BucketName: "x"}}) // no backend
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

// The data plane and the worker build storage clients from storage.backends
// only. A backend registered through the API alone has none, so a bucket on it
// failed every later call with "unknown storage backend". It is refused when
// the bucket is created, with the reason.
func TestCreateBucket_BackendNotConfigured(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true, getTxBucket: validBucket()}
	h := NewHandler(repo, nil, allowAuthorizer{})

	h.SetConfiguredBackends([]string{"elsewhere"})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unconfigured backend: code = %v, want FailedPrecondition", code(err))
	}
	if !strings.Contains(err.Error(), "storage.backends") {
		t.Errorf("error %q does not say where the backend has to be declared", err)
	}

	h.SetConfiguredBackends([]string{validBucket().BackendID})
	if _, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()}); err != nil {
		t.Fatalf("configured backend refused: %v", err)
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

// The two things the repo can refuse a create for now answer differently,
// because they call for opposite responses: a duplicate is done, an
// unregistered backend needs the backend created first. They were both
// ErrConflict → FailedPrecondition, so a client could only tell them apart by
// reading the prose.
func TestCreateBucket_ConflictMapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"unregistered backend", admindomain.ErrConflict, connect.CodeFailedPrecondition},
		{"duplicate bucket", admindomain.ErrAlreadyExists, connect.CodeAlreadyExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{backendEnabled: true, createTxErr: tc.err}
			h := NewHandler(repo, nil, allowAuthorizer{})
			_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
			if code(err) != tc.want {
				t.Fatalf("code = %v, want %v", code(err), tc.want)
			}
		})
	}
}

// EnsureBucket treats a lost create race as success and returns the bucket the
// winner made. The sentinel it matches on moved with this change, and matching
// the old one would still compile — so this pins the branch that is, by
// design, almost impossible to reach on purpose.
func TestEnsureBucket_LostRaceReturnsTheExistingBucket(t *testing.T) {
	repo := &fakeRepo{
		backendEnabled:   true,
		getNotFoundFirst: true,
		createTxErr:      fmt.Errorf("wrapped: %w", admindomain.ErrAlreadyExists),
		getBucket:        admindomain.Bucket{BackendID: "primary", BucketName: "acme"},
	}
	h := NewHandler(repo, nil, allowAuthorizer{})
	got, created, err := h.EnsureBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if err != nil {
		t.Fatalf("a lost race must be success, got %v", err)
	}
	if created {
		t.Error("created = true; the winner created it, not this call")
	}
	if got == nil || got.BucketName != "acme" {
		t.Errorf("got %+v, want the existing bucket", got)
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
		BackendID: "primary", BucketName: "acme", OwnerTenantID: uuid.New(),
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
		BackendID: "primary", BucketName: "acme", OwnerTenantID: uuid.New(),
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
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: true})
	if code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
}

func TestDeleteBucket_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{deleteTxErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, nil, allowAuthorizer{})
	// DeleteOnBackend=false → physical row delete path (DeleteTx) under OCC.
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", ExpectedVersion: 1})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// TestDeleteBucket_RefusesWhileCollectionsBind covers a delete that used to
// reach Postgres and come back as CodeInternal carrying
// `violates foreign key constraint "collections_bucket_id_fkey" (SQLSTATE
// 23503)`. The constraint was the only thing refusing it — this handler had
// no referential check at all — so the API's answer to a foreseeable
// precondition was a database string, and the outbox path (DeleteOnBackend)
// got further still: MarkDeleting committed, then the reconciler retried a
// row delete that could never succeed.
func TestDeleteBucket_RefusesWhileCollectionsBind(t *testing.T) {
	repo := &fakeRepo{bucketRefs: []admindomain.BucketReference{
		{Relation: "collections", Count: 2},
		{Relation: "tenant_default_bindings", Count: 0},
	}}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	for _, onBackend := range []bool{false, true} {
		err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin), DeleteBucketInput{
			BackendID: "primary", BucketName: "acme", DeleteOnBackend: onBackend,
		})
		if code(err) != connect.CodeFailedPrecondition {
			t.Fatalf("DeleteOnBackend=%v: code = %v, want FailedPrecondition (%v)",
				onBackend, code(err), err)
		}
		if msg := err.Error(); !strings.Contains(msg, "2 collection") {
			t.Errorf("DeleteOnBackend=%v: the caller needs the count to act on.\ngot: %v",
				onBackend, msg)
		}
	}
}

// TestDeleteBucket_RefusesForANonCollectionHolder is the case the first
// version of this guard missed, and it is not hypothetical: reproduced
// against the compose stack on 2026-08-30, a bucket with no collections but
// one tenant default binding came back as
// `internal: ... violates foreign key constraint
// "tenant_default_bindings_bucket_id_fkey" (SQLSTATE 23503)` — the exact
// error the guard had been added to remove, entering through the door it did
// not watch.
//
// Six relations hold a bucket under RESTRICT. Counting the most obvious one
// and calling the job done is what made a fixed bug look fixed.
func TestDeleteBucket_RefusesForANonCollectionHolder(t *testing.T) {
	repo := &fakeRepo{bucketRefs: []admindomain.BucketReference{
		{Relation: "collections", Count: 0},
		{Relation: "tenant_default_bindings", Count: 1},
	}}
	h := NewHandler(repo, nil, allowAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme"})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (%v)", code(err), err)
	}
	if msg := err.Error(); !strings.Contains(msg, "tenant_default_bindings") {
		t.Errorf("the message must name what is holding the bucket, or the "+
			"operator is back to guessing.\ngot: %v", msg)
	}
	if msg := err.Error(); strings.Contains(msg, "0 collections") {
		t.Errorf("relations with nothing in them must not be listed.\ngot: %v", msg)
	}
}

// TestDeleteBucket_CountsReferencesCrossTenant pins the half of the guard
// that cannot be seen from its return value. Buckets are platform-level; the
// Collections bound to them are tenant-owned. Under the RLS pool a session
// with no acting tenant sees NONE of those rows, so the count comes back 0
// and the guard waves through exactly the bucket it exists to protect —
// while the FK check, which does not consult RLS, still refuses the delete.
// Measured against a live paladin_app session before this guard was written.
func TestDeleteBucket_CountsReferencesCrossTenant(t *testing.T) {
	repo := &fakeRepo{}
	h := NewHandler(repo, nil, allowAuthorizer{})
	if err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme"}); err != nil {
		t.Fatalf("delete with no references: %v", err)
	}
	if !repo.bucketRefsCross {
		t.Error("the reference count ran tenant-scoped; against the RLS pool it " +
			"would have returned 0 regardless of how many collections bind")
	}
}

// Compile-time interface assertions.
var (
	_ Repository       = (*fakeRepo)(nil)
	_ Provisioner      = okProvisioner{}
	_ cedar.Authorizer = allowAuthorizer{}
)

// Bucket constraints are now enforced on every upload into the bucket, so a
// set no upload could satisfy — a part bound outside S3's, an unknown
// checksum algorithm — is refused when the bucket is created, not discovered
// by the first upload that fails.
func TestCreateBucket_RefusesUnusableConstraints(t *testing.T) {
	b := validBucket()
	b.Constraints = admindomain.BucketConstraints{MinPartSizeBytes: 1 << 20}
	h := NewHandler(&fakeRepo{backendEnabled: true}, okProvisioner{}, allowAuthorizer{})
	if _, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: b}); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("CreateBucket code = %v, want InvalidArgument", code(err))
	}
	if _, _, err := h.EnsureBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: b}); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("EnsureBucket code = %v, want InvalidArgument", code(err))
	}
}
