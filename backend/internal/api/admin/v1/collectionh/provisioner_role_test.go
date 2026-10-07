package collectionh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// provisionerCtx carries ONLY platform.tenant-provisioner — no platform.admin.
func provisionerCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  "provisioner",
		TenantID: tid,
		Roles:    []string{apiutil.RoleTenantProvisioner},
	})
}

// Creating another tenant's object keys is provisioning: it is the step that
// makes a freshly created tenant usable. The authorizer is allowAll so the
// assertion is about the role gate, not about Cedar.
func TestCreateCollection_AllowsTenantProvisionerCrossTenant(t *testing.T) {
	other := uuid.New()
	fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
		return fullKey(a.TenantID, a.Collection), nil
	}}
	h := NewHandler(fr, allowAll())

	got, err := h.CreateCollection(provisionerCtx(uuid.New()), CreateCollectionArgs{
		TenantID: other, Collection: "acme-avatars", BackendID: "aws-eu",
	})
	if err != nil {
		t.Fatalf("cross-tenant CreateCollection as tenant-provisioner: %v", err)
	}
	if got.TenantID != other {
		t.Fatalf("object key landed in tenant %s, want %s", got.TenantID, other)
	}
}

// It reads before it creates — that read is what makes re-running provisioning
// a no-op instead of a conflict.
func TestGetCollection_AllowsTenantProvisionerCrossTenant(t *testing.T) {
	other := uuid.New()
	fr := &fakeRepo{getFn: func(_ context.Context, tid uuid.UUID, key string) (Collection, error) {
		return fullKey(tid, key), nil
	}}
	h := NewHandler(fr, allowAll())

	if _, err := h.GetCollection(provisionerCtx(uuid.New()), other, "acme-avatars"); err != nil {
		t.Fatalf("cross-tenant GetCollection as tenant-provisioner: %v", err)
	}
}

// The role carries no authority to remove or rewrite an object key, and none of
// the widened gates leak into the ones that do.
func TestCollectionMutations_StayPlatformAdminOnly(t *testing.T) {
	other := uuid.New()
	ctx := provisionerCtx(uuid.New())

	t.Run("cross-tenant ListCollections", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListCollections(ctx, ListCollectionsArgs{TenantID: other})
		wantCode(t, err, connect.CodePermissionDenied)
	})
}
