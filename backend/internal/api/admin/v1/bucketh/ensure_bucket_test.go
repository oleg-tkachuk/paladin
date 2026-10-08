package bucketh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// EnsureBucket is idempotent: an existing bucket is a no-op success
// (created=false) and no CreateTx is attempted.
func TestEnsureBucket_ExistingIsNoOp(t *testing.T) {
	repo := &fakeRepo{getBucket: validBucket()} // getErr nil → exists
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	got, created, err := h.EnsureBucket(ctxAs(), CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
	if err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	if created {
		t.Error("created = true, want false (already existed)")
	}
	if got == nil || got.BucketName != "acme-logs" {
		t.Errorf("returned bucket = %+v, want the existing row", got)
	}
	if repo.createTxBucket.BucketName != "" {
		t.Error("must not CreateTx when the bucket already exists")
	}
}

// EnsureBucket creates a missing bucket via the shared provision path and marks
// it pending when provision_on_backend=true.
func TestEnsureBucket_CreatesPending(t *testing.T) {
	b := validBucket()
	repo := &fakeRepo{getErr: admindomain.ErrNotFound, backendEnabled: true, getTxBucket: b}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	_, created, err := h.EnsureBucket(ctxAs(), CreateBucketInput{Bucket: b, ProvisionOnBackend: true})
	if err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if repo.createTxBucket.ProvisionState != admindomain.BucketProvisionStatePending {
		t.Errorf("provision_state = %q, want pending", repo.createTxBucket.ProvisionState)
	}
}

// A tenant must not create backends: an unknown backend → FailedPrecondition.
func TestEnsureBucket_UnknownBackend(t *testing.T) {
	repo := &fakeRepo{getErr: admindomain.ErrNotFound, backendEnaErr: admindomain.ErrNotFound}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	_, _, err := h.EnsureBucket(ctxAs(), CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", code(err))
	}
}

// provision_on_backend=true with no provisioner wired → Unavailable.
func TestEnsureBucket_ProvisionWithoutProvisioner(t *testing.T) {
	repo := &fakeRepo{getErr: admindomain.ErrNotFound, backendEnabled: true}
	h := NewHandler(repo, nil, allowAuthorizer{})
	_, _, err := h.EnsureBucket(ctxAs(), CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
	if code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
}

// No principal → Unauthenticated (EnsureBucket still requires an authenticated
// caller even though it skips the role/Cedar gate).
func TestEnsureBucket_NoPrincipal(t *testing.T) {
	h := NewHandler(&fakeRepo{}, okProvisioner{}, allowAuthorizer{})
	_, _, err := h.EnsureBucket(context.Background(), CreateBucketInput{Bucket: validBucket()})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
}
