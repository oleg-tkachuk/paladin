package collectionh

import (
	"context"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// A tenant provisioner creating another tenant's collection is admitted to it
// by the policy decision on that tenant, so the audit row is filed in that
// tenant's trail; a caller the role gate refuses admits nothing.
func TestTenantProvisionerAdmitsTheTenantItProvisions(t *testing.T) {
	other := uuid.New()
	fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
		return fullKey(a.TenantID, a.Collection), nil
	}}
	h := NewHandler(fr, allowAll())
	args := CreateCollectionArgs{TenantID: other, Collection: "acme-avatars", BackendID: "aws-eu"}

	ctx := apiutil.WithAdmissionSlot(provisionerCtx(uuid.New()))
	if _, err := h.CreateCollection(ctx, args); err != nil {
		t.Fatalf("create as tenant-provisioner: %v", err)
	}
	if !slices.Contains(apiutil.AdmittedTenants(ctx), other) {
		t.Errorf("admitted %v, want %s among them", apiutil.AdmittedTenants(ctx), other)
	}

	refused := apiutil.WithAdmissionSlot(authedCtx(uuid.New()))
	if _, err := h.CreateCollection(refused, args); err == nil {
		t.Fatal("a tenant principal created another tenant's collection")
	}
	if got := apiutil.AdmittedTenants(refused); slices.Contains(got, other) {
		t.Errorf("a refused caller was admitted to %s", other)
	}
}
