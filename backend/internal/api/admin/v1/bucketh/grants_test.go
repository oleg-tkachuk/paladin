package bucketh

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// fakeGrants records what the handler asks of the grant repository.
type fakeGrants struct {
	granted   map[uuid.UUID]bool
	grantedBy string
	txGrants  []uuid.UUID
	revokeErr error
	revokeCtx context.Context
}

func (f *fakeGrants) Grant(_ context.Context, _, _ string, tenant uuid.UUID, by string) (admindomain.BucketGrant, error) {
	if f.granted == nil {
		f.granted = map[uuid.UUID]bool{}
	}
	f.granted[tenant], f.grantedBy = true, by
	return admindomain.BucketGrant{TenantID: tenant, GrantedBy: by, GrantedAt: time.Now()}, nil
}

func (f *fakeGrants) GrantTx(ctx context.Context, _ pgx.Tx, b, n string, tenant uuid.UUID, by string) (admindomain.BucketGrant, error) {
	f.txGrants = append(f.txGrants, tenant)
	return f.Grant(ctx, b, n, tenant, by)
}

func (f *fakeGrants) RevokeGrant(ctx context.Context, _, _ string, tenant uuid.UUID) error {
	f.revokeCtx = ctx
	if f.revokeErr != nil {
		return f.revokeErr
	}
	delete(f.granted, tenant)
	return nil
}

func (f *fakeGrants) ListGrants(context.Context, string, string) ([]admindomain.BucketGrant, error) {
	out := []admindomain.BucketGrant{}
	for t := range f.granted {
		out = append(out, admindomain.BucketGrant{TenantID: t})
	}
	return out, nil
}

func (f *fakeGrants) MayBind(_ context.Context, _, _ string, tenant uuid.UUID) (bool, error) {
	return f.granted[tenant], nil
}

// ownerAuthorizer allows everything and records the owner each decision saw.
type ownerAuthorizer struct{ owners []uuid.UUID }

func (a *ownerAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.owners = append(a.owners, r.OwnerTenantID)
	return cedar.DecisionAllow, nil
}

func grantsHandler(repo *fakeRepo, grants *fakeGrants, authz cedar.Authorizer) *Handler {
	h := NewHandler(repo, okProvisioner{onBackend: true}, authz)
	h.SetGrants(grants)
	return h
}

func wantReason(t *testing.T, err error, c connect.Code, r commonv1.ErrorReason) {
	t.Helper()
	if code(err) != c || reasonOf(t, err) != r {
		t.Fatalf("err = %v (reason %v), want %v %v", err, reasonOf(t, err), c, r)
	}
}

// Every bucket has an owner (ADR-0032): a create naming none is refused and
// writes nothing.
func TestCreateBucketRequiresAnOwner(t *testing.T) {
	repo := &fakeRepo{backendEnabled: true}
	h := grantsHandler(repo, &fakeGrants{}, allowAuthorizer{})
	b := validBucket()
	b.OwnerTenantID = uuid.Nil()
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin), CreateBucketInput{Bucket: b, ProvisionOnBackend: true})
	wantReason(t, err, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_BUCKET_OWNER_REQUIRED)
	if repo.createTxBucket.BucketName != "" {
		t.Fatal("a row was written for a bucket with no owner")
	}
}

// Self-service takes an existing bucket only when its tenant owns it or holds
// a grant: naming another tenant's bucket no longer hands it over.
func TestEnsureBucketRefusesAnotherTenantsBucket(t *testing.T) {
	existing := validBucket()
	caller := uuid.New()
	in := CreateBucketInput{Bucket: admindomain.Bucket{
		BackendID: existing.BackendID, BucketName: existing.BucketName, OwnerTenantID: caller,
	}, ProvisionOnBackend: true}

	grants := &fakeGrants{}
	h := grantsHandler(&fakeRepo{backendEnabled: true, getBucket: existing}, grants, allowAuthorizer{})
	_, _, err := h.EnsureBucket(ctxAs(), in)
	wantReason(t, err, connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_GRANTED)

	grants.granted = map[uuid.UUID]bool{caller: true}
	got, created, err := h.EnsureBucket(ctxAs(), in)
	if err != nil || created || got.OwnerTenantID != existing.OwnerTenantID {
		t.Fatalf("with a grant: %+v, created=%v, %v; want the existing bucket, untouched", got, created, err)
	}
}

// A grant is written for another tenant, recorded as made by the caller, and
// authorized against the bucket's stored owner; the owner itself is refused.
func TestGrantBucket(t *testing.T) {
	stored := validBucket()
	other := uuid.New()
	grants := &fakeGrants{}
	authz := &ownerAuthorizer{}
	h := grantsHandler(&fakeRepo{getBucket: stored}, grants, authz)

	if _, err := h.GrantBucket(ctxAs(apiutil.RoleTenantUser), stored.BackendID, stored.BucketName, other); code(err) != connect.CodePermissionDenied {
		t.Fatalf("a tenant user: %v, want PermissionDenied", err)
	}
	// A tenant provisioner lets the tenants it provisions into the pool...
	if _, err := h.GrantBucket(ctxAs(apiutil.RoleTenantProvisioner), stored.BackendID, stored.BucketName, uuid.New()); err != nil {
		t.Fatalf("a tenant provisioner granting: %v", err)
	}
	// ...and takes nothing back.
	if err := h.RevokeBucketGrant(ctxAs(apiutil.RoleTenantProvisioner), stored.BackendID, stored.BucketName, other); code(err) != connect.CodePermissionDenied {
		t.Fatalf("a tenant provisioner revoking: %v, want PermissionDenied", err)
	}
	g, err := h.GrantBucket(ctxAs(apiutil.RoleBucketAdmin), stored.BackendID, stored.BucketName, other)
	if err != nil || g.TenantID != other || grants.grantedBy != "tester" {
		t.Fatalf("grant = %+v by %q, %v; want the tenant, by the caller", g, grants.grantedBy, err)
	}
	if len(authz.owners) == 0 || authz.owners[len(authz.owners)-1] != stored.OwnerTenantID {
		t.Fatalf("authorized against %v, want the stored owner %s", authz.owners, stored.OwnerTenantID)
	}
	if _, err := h.GrantBucket(ctxAs(apiutil.RoleBucketAdmin), stored.BackendID, stored.BucketName, stored.OwnerTenantID); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("granting the owner: %v, want InvalidArgument", err)
	}
}

// A grant whose tenant still has collections is not revoked; the question is
// asked across tenants, or row-level security would hide the collections.
func TestRevokeBucketGrant(t *testing.T) {
	stored := validBucket()
	grants := &fakeGrants{revokeErr: admindomain.ErrBucketGrantInUse}
	h := grantsHandler(&fakeRepo{getBucket: stored}, grants, allowAuthorizer{})
	err := h.RevokeBucketGrant(ctxAs(apiutil.RoleBucketAdmin), stored.BackendID, stored.BucketName, uuid.New())
	wantReason(t, err, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_BUCKET_GRANT_IN_USE)
	if !auth.CrossTenantRead(grants.revokeCtx) {
		t.Fatal("the revoke did not read across tenants")
	}
}

// Moving a bucket to another owner is authorized against the stored owner
// and the new one, leaves the previous owner a grant in the same transaction,
// and refuses to move it to no owner at all.
func TestUpdateBucketMovesTheOwner(t *testing.T) {
	stored := validBucket()
	next := uuid.New()
	grants := &fakeGrants{}
	authz := &ownerAuthorizer{}
	repo := &fakeRepo{getBucket: stored, getTxBucket: stored}
	h := grantsHandler(repo, grants, authz)
	moved := stored
	moved.OwnerTenantID = next

	if _, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{
		Bucket: moved, ExpectedVersion: 1, UpdateMask: []string{MaskOwnerTenantID},
	}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(authz.owners) != 2 || authz.owners[0] != stored.OwnerTenantID || authz.owners[1] != next {
		t.Fatalf("authorized against %v, want the stored owner then the new one", authz.owners)
	}
	if len(grants.txGrants) != 1 || grants.txGrants[0] != stored.OwnerTenantID {
		t.Fatalf("grants in the move's transaction = %v, want the previous owner", grants.txGrants)
	}

	// A display-name change leaves the owner, and grants nothing.
	grants.txGrants = nil
	renamed := stored
	renamed.DisplayName = "logs"
	if _, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{
		Bucket: renamed, ExpectedVersion: 1, UpdateMask: []string{"display_name"},
	}); err != nil || len(grants.txGrants) != 0 {
		t.Fatalf("rename: %v, grants %v; want no grant", err, grants.txGrants)
	}

	ownerless := stored
	ownerless.OwnerTenantID = uuid.Nil()
	_, err := h.UpdateBucket(ctxAs(apiutil.RoleBucketAdmin), UpdateBucketInput{
		Bucket: ownerless, ExpectedVersion: 1, UpdateMask: []string{MaskOwnerTenantID},
	})
	wantReason(t, err, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_BUCKET_OWNER_REQUIRED)
}
