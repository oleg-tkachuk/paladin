package bucketh

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/apiutil"
)

// A consumer's tenant is unusable until its bucket exists, and provisioning has
// to run without a human (ADR-0011). Creating a bucket only adds a destination.
func TestCreateBucket_AllowsTenantProvisioner(t *testing.T) {
	b := validBucket()
	h := NewHandler(&fakeRepo{backendEnabled: true, getTxBucket: b}, okProvisioner{}, allowAuthorizer{})

	if _, err := h.CreateBucket(ctxAs(apiutil.RoleTenantProvisioner),
		CreateBucketInput{Bucket: b}); err != nil {
		t.Fatalf("CreateBucket as tenant-provisioner: %v", err)
	}
}

// It reads before it creates — that read is what makes adopting an existing
// bucket a no-op instead of a conflict.
func TestGetBucket_AllowsTenantProvisioner(t *testing.T) {
	b := validBucket()
	b.CedarPolicy = `permit (principal, action, resource);`
	h := NewHandler(&fakeRepo{getBucket: b}, okProvisioner{}, allowAuthorizer{})

	got, err := h.GetBucket(ctxAs(apiutil.RoleTenantProvisioner), b.BackendID, b.BucketName)
	if err != nil {
		t.Fatalf("GetBucket as tenant-provisioner: %v", err)
	}
	// Same redacted view tenant admins get: a provisioner has no business
	// reading another tenant's policy graph.
	if got.CedarPolicy != "" {
		t.Fatal("a provisioner must not see bucket policy text")
	}
}

// Reconfiguring or removing a bucket is not provisioning. A leaked provisioner
// credential must not be able to point a bucket somewhere else or delete it.
func TestBucketMutations_StayBucketAdminOnly(t *testing.T) {
	ctx := ctxAs(apiutil.RoleTenantProvisioner)
	b := validBucket()

	t.Run("DeleteBucket", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, okProvisioner{}, allowAuthorizer{})
		err := h.DeleteBucket(ctx, DeleteBucketInput{
			BackendID: b.BackendID, BucketName: b.BucketName, ExpectedVersion: 1,
		})
		if code(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", code(err))
		}
	})

	t.Run("ListBuckets", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, okProvisioner{}, allowAuthorizer{})
		_, _, err := h.ListBuckets(ctx, admindomain.ListBucketsArgs{BackendID: b.BackendID})
		if code(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", code(err))
		}
	})
}
