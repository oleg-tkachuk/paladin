package bucketh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// ─── Extra test doubles ──────────────────────────────────────────────────────

// errAuthorizer returns a non-decision error so tests can reach the
// authorize() internal-error branch (mapped to CodeInternal).
type errAuthorizer struct{}

func (errAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, errors.New("cedar engine boom")
}

// fakeEvents records the last event dispatched on the tx seam so tests can
// assert the handler emits the right type/resource/payload, and can force a
// dispatch failure to prove the surrounding tx rolls back.
type fakeEvents struct {
	dispatchTxErr error
	txCalls       int
	lastEvent     worker.Event
	lastTenantID  string
}

func (f *fakeEvents) Dispatch(context.Context, string, worker.Event) (int, error) {
	return 0, nil
}

func (f *fakeEvents) DispatchTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) (int, error) {
	f.txCalls++
	f.lastTenantID = tenantID
	f.lastEvent = evt
	return 1, f.dispatchTxErr
}

// ctxTenant builds an authed context with an explicit tenant id, so tests that
// compare the caller tenant against a request argument (ListAccessibleBuckets)
// can control both sides.
func ctxTenant(tid uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: tid, Roles: roles,
	})
}

// ─── authorize() internal-error branch ───────────────────────────────────────

func TestGetBucket_AuthorizerError(t *testing.T) {
	repo := &fakeRepo{getBucket: validBucket()}
	h := NewHandler(repo, nil, errAuthorizer{})
	_, err := h.GetBucket(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme")
	if code(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal (authz engine error)", code(err))
	}
}

// ─── ListBuckets ─────────────────────────────────────────────────────────────

func TestListBuckets_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, _, err := h.ListBuckets(ctxAs(apiutil.RoleTenantUser), admindomain.ListBucketsArgs{})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestListBuckets_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	_, _, err := h.ListBuckets(ctxAs(apiutil.RoleBucketAdmin), admindomain.ListBucketsArgs{BackendID: "primary"})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestListBuckets_ForwardsArgsAndReturns(t *testing.T) {
	want := []admindomain.Bucket{{BackendID: "primary", BucketName: "a"}, {BackendID: "primary", BucketName: "b"}}
	repo := &fakeRepo{listResult: want, listToken: "next-page"}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	args := admindomain.ListBucketsArgs{BackendID: "primary", PageSize: 7, AfterName: "a"}
	got, token, err := h.ListBuckets(ctxAs(apiutil.RolePlatformAdmin), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || token != "next-page" {
		t.Fatalf("got (%d buckets, token=%q), want (2, next-page)", len(got), token)
	}
	if repo.listArgs.BackendID != "primary" || repo.listArgs.PageSize != 7 || repo.listArgs.AfterName != "a" {
		t.Errorf("forwarded args = %+v, want backend=primary pageSize=7 afterName=a", repo.listArgs)
	}
}

// ─── ListAccessibleBuckets ───────────────────────────────────────────────────

func TestListAccessibleBuckets_Unauthenticated(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, _, err := h.ListAccessibleBuckets(context.Background(), uuid.New(), 10, "", "")
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated (no principal)", code(err))
	}
}

func TestListAccessibleBuckets_CrossTenantForbidden(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	// Non-platform-admin asking for a tenant that isn't its own.
	_, _, err := h.ListAccessibleBuckets(ctxTenant(caller, apiutil.RoleBucketAdmin), other, 10, "", "")
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cross-tenant)", code(err))
	}
}

func TestListAccessibleBuckets_PlatformAdminCrossTenantForwards(t *testing.T) {
	caller := uuid.New()
	target := uuid.New()
	want := []admindomain.Bucket{{BackendID: "primary", BucketName: "shared"}}
	repo := &fakeRepo{listAccResult: want, listAccToken: "tok"}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	got, token, err := h.ListAccessibleBuckets(ctxTenant(caller, apiutil.RolePlatformAdmin), target, 5, "primary", "shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || token != "tok" {
		t.Fatalf("got (%d, %q), want (1, tok)", len(got), token)
	}
	if repo.listAccTenant != target {
		t.Errorf("forwarded tenant = %v, want %v (the requested target, not caller)", repo.listAccTenant, target)
	}
	if repo.listAccPageSize != 5 || repo.listAccAfterBk != "primary" || repo.listAccAfterNm != "shared" {
		t.Errorf("forwarded paging = (size=%d, afterBk=%q, afterNm=%q), want (5, primary, shared)",
			repo.listAccPageSize, repo.listAccAfterBk, repo.listAccAfterNm)
	}
}

func TestListAccessibleBuckets_SameTenantAllowed(t *testing.T) {
	caller := uuid.New()
	repo := &fakeRepo{listAccResult: []admindomain.Bucket{{BucketName: "own"}}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	got, _, err := h.ListAccessibleBuckets(ctxTenant(caller, apiutil.RoleTenantAdmin), caller, 3, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || repo.listAccTenant != caller {
		t.Errorf("got %d buckets for tenant %v, want 1 for %v", len(got), repo.listAccTenant, caller)
	}
}

func TestListAccessibleBuckets_CedarDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	// Same-tenant so the caller-check passes and we reach the cedar gate.
	_, _, err := h.ListAccessibleBuckets(ctxTenant(caller, apiutil.RoleTenantAdmin), caller, 10, "", "")
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

// ─── UpdateBucket ────────────────────────────────────────────────────────────

func TestUpdateBucket_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.UpdateBucket(ctxAs(apiutil.RoleTenantAdmin), UpdateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (tenant.admin cannot mutate)", code(err))
	}
}

func TestUpdateBucket_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	_, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestUpdateBucket_ForwardsArgsAndReturnsReadBack(t *testing.T) {
	in := validBucket()
	readBack := admindomain.Bucket{BackendID: "primary", BucketName: "acme-logs", ResourceVersion: 9}
	repo := &fakeRepo{getTxBucket: readBack}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	got, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{
		Bucket: in, ExpectedVersion: 4, UpdateMask: []string{"region"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ResourceVersion != 9 {
		t.Errorf("returned resource_version = %d, want 9 (the read-back row)", got.ResourceVersion)
	}
	if repo.gotUpdateVer != 4 || len(repo.gotUpdateMask) != 1 || repo.gotUpdateMask[0] != "region" {
		t.Errorf("forwarded (ver=%d, mask=%v), want (4, [region])", repo.gotUpdateVer, repo.gotUpdateMask)
	}
	if repo.gotUpdateBucket.BucketName != "acme-logs" {
		t.Errorf("forwarded bucket = %q, want acme-logs", repo.gotUpdateBucket.BucketName)
	}
}

func TestUpdateBucket_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{updateBasicTxErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{Bucket: validBucket(), ExpectedVersion: 1})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── SetPolicy ───────────────────────────────────────────────────────────────

func TestSetPolicy_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.SetPolicy(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme", "permit();", 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestSetPolicy_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	_, err := h.SetPolicy(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", "permit();", 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestSetPolicy_ForwardsArgs(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	got, err := h.SetPolicy(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", "permit(principal,action,resource);", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BucketName != "acme" {
		t.Errorf("returned bucket = %q, want acme", got.BucketName)
	}
	if repo.gotPolicyValue != "permit(principal,action,resource);" {
		t.Errorf("forwarded policy = %q, want the input policy", repo.gotPolicyValue)
	}
	if repo.gotPolicyArgs != (setterArgs{"primary", "acme", 3}) {
		t.Errorf("forwarded args = %+v, want {primary acme 3}", repo.gotPolicyArgs)
	}
}

func TestSetPolicy_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{setPolicyErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetPolicy(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", "permit();", 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── SetLifecycleRules ───────────────────────────────────────────────────────

func TestSetLifecycleRules_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.SetLifecycleRules(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme", nil, 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestSetLifecycleRules_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	_, err := h.SetLifecycleRules(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", nil, 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestSetLifecycleRules_InvalidCELRejected(t *testing.T) {
	repo := &fakeRepo{}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	// A non-bool CEL expression must be rejected up front (Validate requires bool).
	rules := []admindomain.LifecycleRule{{ID: "r1", Match: "1 + 1"}}
	_, err := h.SetLifecycleRules(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", rules, 1)
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (bad CEL)", code(err))
	}
	if repo.gotLifecycleArg != nil {
		t.Error("repo.SetLifecycle must not be called when a rule fails validation")
	}
}

func TestSetLifecycleRules_ValidRulesForwarded(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	// Empty Match is the "match everything" sentinel — valid.
	rules := []admindomain.LifecycleRule{{ID: "expire-30d", Match: ""}, {ID: "expire-90d", Match: ""}}
	got, err := h.SetLifecycleRules(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", rules, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BucketName != "acme" {
		t.Errorf("returned bucket = %q, want acme", got.BucketName)
	}
	if len(repo.gotLifecycleArg) != 2 || repo.gotLifecycleArg[0].ID != "expire-30d" {
		t.Errorf("forwarded rules = %+v, want the 2 input rules", repo.gotLifecycleArg)
	}
}

func TestSetLifecycleRules_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{setLifecycleErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetLifecycleRules(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", nil, 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── SetObjectLock ───────────────────────────────────────────────────────────

func TestSetObjectLock_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.SetObjectLock(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme", admindomain.ObjectLockConfig{}, 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestSetObjectLock_ForwardsArgs(t *testing.T) {
	// Versioning on: object lock attaches retention to a version, so enabling
	// it without versioning is refused (see the next test).
	repo := &fakeRepo{getBucket: admindomain.Bucket{
		BucketName: "acme",
		Versioning: admindomain.BucketVersioning{Enabled: true},
	}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	lock := admindomain.ObjectLockConfig{Enabled: true, DefaultMode: "COMPLIANCE"}
	if _, err := h.SetObjectLock(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", lock, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotLockArg != lock {
		t.Errorf("forwarded lock = %+v, want %+v", repo.gotLockArg, lock)
	}
}

// TestSetObjectLock_RequiresVersioning pins the precondition that keeps the
// feature coherent: a lock is attached to a version, so a bucket without
// versioning has nothing to attach one to. Without this check the bucket
// reports object lock as enabled while every SetObjectRetention against it
// fails on "no current version".
func TestSetObjectLock_RequiresVersioning(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}} // versioning off
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetObjectLock(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme",
		admindomain.ObjectLockConfig{Enabled: true}, 1)
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", code(err))
	}
	if repo.gotLockArg.Enabled {
		t.Error("the refused call still reached the repository")
	}
}

// TestSetObjectLock_DisableNeedsNoVersioning pins the asymmetry: turning
// object lock OFF must work on any bucket, including one whose versioning was
// somehow already off — otherwise a misconfigured bucket has no way back.
func TestSetObjectLock_DisableNeedsNoVersioning(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	if _, err := h.SetObjectLock(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme",
		admindomain.ObjectLockConfig{Enabled: false}, 1); err != nil {
		t.Fatalf("disabling object lock was refused: %v", err)
	}
}

// TestSetVersioning_RefusedWhileObjectLockOn pins the other half. Turning
// versioning off under a lock-enabled bucket would strand every existing
// retention row: the trigger keeps enforcing them and nothing can create the
// version a future lock needs.
func TestSetVersioning_RefusedWhileObjectLockOn(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{
		BucketName: "acme",
		ObjectLock: admindomain.ObjectLockConfig{Enabled: true},
	}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetVersioning(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme",
		admindomain.BucketVersioning{Enabled: false}, 1)
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", code(err))
	}
}

func TestSetObjectLock_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{setLockErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetObjectLock(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", admindomain.ObjectLockConfig{}, 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── SetVersioning ───────────────────────────────────────────────────────────

func TestSetVersioning_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.SetVersioning(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme", admindomain.BucketVersioning{}, 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestSetVersioning_ForwardsArgs(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	v := admindomain.BucketVersioning{Enabled: true, KeepDeletesForever: true}
	if _, err := h.SetVersioning(ctxAs(apiutil.RolePlatformAdmin), "primary", "acme", v, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotVersioningArg != v {
		t.Errorf("forwarded versioning = %+v, want %+v", repo.gotVersioningArg, v)
	}
}

func TestSetVersioning_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{setVersioningErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetVersioning(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", admindomain.BucketVersioning{}, 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── SetReplication ──────────────────────────────────────────────────────────

func TestSetReplication_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	_, err := h.SetReplication(ctxAs(apiutil.RoleTenantAdmin), "primary", "acme", admindomain.BucketReplication{}, 1)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestSetReplication_ForwardsArgs(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{BucketName: "acme"}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	r := admindomain.BucketReplication{Enabled: true, DestinationBucket: "dr-bucket"}
	if _, err := h.SetReplication(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", r, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.gotReplicationArg != r {
		t.Errorf("forwarded replication = %+v, want %+v", repo.gotReplicationArg, r)
	}
}

func TestSetReplication_VersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{setReplicationErr: admindomain.ErrVersionMismatch}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	_, err := h.SetReplication(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", admindomain.BucketReplication{}, 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── DeleteBucket — extra branches ───────────────────────────────────────────

func TestDeleteBucket_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleTenantAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme"})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestDeleteBucket_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, denyAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme"})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestDeleteBucket_ImmediateSucceeds(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{
		BackendID: "primary", BucketName: "acme", OwnerTenantID: uuid.New(), CreatedOnBackend: true,
	}}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.deleteTxCalled || repo.markDeletingCalled {
		t.Errorf("immediate mode should DeleteTx (called=%v) and not MarkDeletingTx (called=%v)",
			repo.deleteTxCalled, repo.markDeletingCalled)
	}
}

func TestDeleteBucket_OutboxMarksDeleting(t *testing.T) {
	repo := &fakeRepo{getBucket: admindomain.Bucket{
		BackendID: "primary", BucketName: "acme", OwnerTenantID: uuid.New(), CreatedOnBackend: true,
	}}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	// DeleteOnBackend=true + provisioner wired → outbox path (MarkDeletingTx).
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.markDeletingCalled || repo.deleteTxCalled {
		t.Errorf("outbox mode should MarkDeletingTx (called=%v) and not DeleteTx (called=%v)",
			repo.markDeletingCalled, repo.deleteTxCalled)
	}
}

func TestDeleteBucket_OutboxVersionMismatchAborts(t *testing.T) {
	repo := &fakeRepo{
		getBucket:         admindomain.Bucket{BackendID: "primary", BucketName: "acme", CreatedOnBackend: true},
		markDeletingTxErr: admindomain.ErrVersionMismatch,
	}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: true, ExpectedVersion: 1})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── Event fan-out (SetEventProducer + dispatchEventTx) ───────────────────────

func TestCreateBucket_DispatchesCreatedEvent(t *testing.T) {
	owner := uuid.New()
	readBack := admindomain.Bucket{BackendID: "primary", BucketName: "acme-logs", OwnerTenantID: owner, Region: "us-east-1"}
	repo := &fakeRepo{backendEnabled: true, getTxBucket: readBack}
	ev := &fakeEvents{}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	h.SetEventProducer(ev)

	if _, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: validBucket()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.txCalls != 1 {
		t.Fatalf("DispatchTx calls = %d, want 1", ev.txCalls)
	}
	if ev.lastEvent.Type != "paladin.bucket.created" {
		t.Errorf("event type = %q, want paladin.bucket.created", ev.lastEvent.Type)
	}
	if ev.lastEvent.ActorSubject != "tester" {
		t.Errorf("actor = %q, want tester (from principal)", ev.lastEvent.ActorSubject)
	}
	wantRes := bucketResourceName(owner, "primary", "acme-logs")
	if ev.lastEvent.ResourceName != wantRes {
		t.Errorf("resource = %q, want %q", ev.lastEvent.ResourceName, wantRes)
	}
	if ev.lastEvent.Payload["bucket_name"] != "acme-logs" || ev.lastEvent.Payload["region"] != "us-east-1" {
		t.Errorf("payload = %+v, want bucket_name/region from the read-back row", ev.lastEvent.Payload)
	}
}

func TestCreateBucket_DispatchErrorRollsBack(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true, getTxBucket: validBucket()}
	ev := &fakeEvents{dispatchTxErr: errors.New("outbox insert failed")}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	h.SetEventProducer(ev)

	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: validBucket()})
	if err == nil {
		t.Fatal("expected error when the outbox dispatch fails inside the tx")
	}
	// Non-sentinel error falls through MapError to Internal.
	if code(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal (dispatch failure)", code(err))
	}
}

func TestDeleteBucket_ImmediateDispatchesDeletedEvent(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{getBucket: admindomain.Bucket{BackendID: "primary", BucketName: "acme", OwnerTenantID: owner}}
	ev := &fakeEvents{}
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	h.SetEventProducer(ev)

	if err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: false}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.lastEvent.Type != "paladin.bucket.deleted" {
		t.Errorf("event type = %q, want paladin.bucket.deleted", ev.lastEvent.Type)
	}
	if ev.lastEvent.Payload["mode"] != "immediate" {
		t.Errorf("payload mode = %v, want immediate", ev.lastEvent.Payload["mode"])
	}
}

func TestDeleteBucket_OutboxDispatchesDeletingEvent(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{getBucket: admindomain.Bucket{BackendID: "primary", BucketName: "acme", OwnerTenantID: owner, CreatedOnBackend: true}}
	ev := &fakeEvents{}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	h.SetEventProducer(ev)

	if err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.lastEvent.Type != "paladin.bucket.deleting" {
		t.Errorf("event type = %q, want paladin.bucket.deleting (row not gone yet)", ev.lastEvent.Type)
	}
	if ev.lastEvent.Payload["mode"] != "outbox" {
		t.Errorf("payload mode = %v, want outbox", ev.lastEvent.Payload["mode"])
	}
}

// ─── SetLogger guard ─────────────────────────────────────────────────────────

func TestSetLogger_NilIgnored(t *testing.T) {
	h := NewHandler(&fakeRepo{}, nil, allowAuthorizer{})
	// nil must be a no-op (keeps the nop logger); a real logger must be accepted.
	// Both branches are exercised; the handler must remain usable afterward.
	h.SetLogger(nil)
	h.SetLogger(zap.NewExample())
	if _, err := h.GetBucket(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme"); err != nil {
		t.Fatalf("handler unusable after SetLogger: %v", err)
	}
}

// Compile-time assertion: fakeEvents satisfies the producer seam.
var _ EventProducer = (*fakeEvents)(nil)
