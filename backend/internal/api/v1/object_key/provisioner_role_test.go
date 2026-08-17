package objectkey

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
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
func TestCreateObjectKey_AllowsTenantProvisionerCrossTenant(t *testing.T) {
	other := uuid.New()
	fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateObjectKeyArgs) (ObjectKey, error) {
		return fullKey(a.TenantID, a.ObjectKey), nil
	}}
	h := NewHandler(fr, allowAll())

	got, err := h.CreateObjectKey(provisionerCtx(uuid.New()), CreateObjectKeyArgs{
		TenantID: other, ObjectKey: "acme-avatars", BackendID: "aws-eu",
	})
	if err != nil {
		t.Fatalf("cross-tenant CreateObjectKey as tenant-provisioner: %v", err)
	}
	if got.TenantID != other {
		t.Fatalf("object key landed in tenant %s, want %s", got.TenantID, other)
	}
}

// It reads before it creates — that read is what makes re-running provisioning
// a no-op instead of a conflict.
func TestGetObjectKey_AllowsTenantProvisionerCrossTenant(t *testing.T) {
	other := uuid.New()
	fr := &fakeRepo{getFn: func(_ context.Context, tid uuid.UUID, key string) (ObjectKey, error) {
		return fullKey(tid, key), nil
	}}
	h := NewHandler(fr, allowAll())

	if _, err := h.GetObjectKey(provisionerCtx(uuid.New()), other, "acme-avatars"); err != nil {
		t.Fatalf("cross-tenant GetObjectKey as tenant-provisioner: %v", err)
	}
}

// The role carries no authority to remove or rewrite an object key, and none of
// the widened gates leak into the ones that do.
func TestObjectKeyMutations_StayPlatformAdminOnly(t *testing.T) {
	other := uuid.New()
	ctx := provisionerCtx(uuid.New())

	t.Run("cross-tenant ListObjectKeys", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListObjectKeys(ctx, ListObjectKeysArgs{TenantID: other})
		wantCode(t, err, connect.CodePermissionDenied)
	})
}
