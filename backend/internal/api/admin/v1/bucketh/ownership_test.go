package bucketh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

const probePrefix = "paladin-probe-"

var errStoreDown = errors.New("store down")

// failingExists fails the backend's existence check.
type failingExists struct{ okProvisioner }

func (failingExists) BucketExists(context.Context, string, string) (bool, error) {
	return false, errStoreDown
}

func ownershipHandler(repo *fakeRepo, p Provisioner) *Handler {
	h := NewHandler(repo, p, allowAuthorizer{})
	h.SetReservedBuckets(ReservedBuckets{Prefix: probePrefix})
	return h
}

// provision_on_backend took any bucket the backend held — the backend's own
// included — and a public one then had anonymous reads set on everything in
// it; adopting is now explicit, and an adopted bucket is never public. A row is written only for a bucket Paladin means to take.
func TestCreateBucketTakesOnlyWhatItMeansTo(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bucket    string
		provision bool
		onBackend bool
		want      connect.Code
		reason    commonv1.ErrorReason
	}{
		{"create a new bucket", "acme-logs", true, false, 0, 0},
		{"create one that exists", "acme-logs", true, true, connect.CodeAlreadyExists,
			commonv1.ErrorReason_ERROR_REASON_BUCKET_EXISTS_ON_BACKEND},
		{"register an existing bucket", "acme-logs", false, true, 0, 0},
		{"register one that does not exist", "acme-logs", false, false, connect.CodeFailedPrecondition,
			commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_ON_BACKEND},
		{"create a probe's scratch bucket", probePrefix + "x", true, false, connect.CodeFailedPrecondition,
			commonv1.ErrorReason_ERROR_REASON_BUCKET_RESERVED},
		{"register a probe's scratch bucket", probePrefix + "x", false, true, connect.CodeFailedPrecondition,
			commonv1.ErrorReason_ERROR_REASON_BUCKET_RESERVED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := admindomain.Bucket{BackendID: "primary", BucketName: tc.bucket}
			repo := &fakeRepo{backendEnabled: true, getTxBucket: b}
			h := ownershipHandler(repo, okProvisioner{onBackend: tc.onBackend})
			_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
				CreateBucketInput{Bucket: b, ProvisionOnBackend: tc.provision})
			if tc.want == 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got := repo.createTxBucket.CreatedOnBackend; got != tc.provision {
					t.Fatalf("created_on_backend = %v, want %v", got, tc.provision)
				}
				return
			}
			if code(err) != tc.want || reasonOf(t, err) != tc.reason {
				t.Fatalf("err = %v (reason %v), want %v %v", err, reasonOf(t, err), tc.want, tc.reason)
			}
			if repo.createTxBucket.BucketName != "" {
				t.Fatal("a row was written for a refused bucket")
			}
		})
	}
}

// Whether the bucket exists decides what is allowed, so a store that cannot
// say refuses rather than guesses.
func TestCreateBucketRefusesWhenTheStoreCannotSay(t *testing.T) {
	h := ownershipHandler(&fakeRepo{backendEnabled: true}, failingExists{})
	_, err := h.CreateBucket(ctxAs(apiutil.RoleBucketAdmin),
		CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
	if code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v (%v), want unavailable", code(err), err)
	}
}

// A tenant ensures the shared bucket it names: creating it is self-service,
// taking an existing one is not.
func TestEnsureBucketCreatesButDoesNotAdopt(t *testing.T) {
	t.Run("an existing row is a no-op", func(t *testing.T) {
		row := validBucket()
		h := ownershipHandler(&fakeRepo{backendEnabled: true, getBucket: row}, okProvisioner{onBackend: true})
		_, created, err := h.EnsureBucket(ctxAs(apiutil.RoleBucketAdmin),
			CreateBucketInput{Bucket: row, ProvisionOnBackend: true})
		if err != nil || created {
			t.Fatalf("created = %v, err = %v; want a no-op", created, err)
		}
	})
	t.Run("an unregistered bucket the backend holds", func(t *testing.T) {
		repo := &fakeRepo{backendEnabled: true, getErr: admindomain.ErrNotFound}
		h := ownershipHandler(repo, okProvisioner{onBackend: true})
		_, _, err := h.EnsureBucket(ctxAs(apiutil.RoleBucketAdmin),
			CreateBucketInput{Bucket: validBucket(), ProvisionOnBackend: true})
		if reasonOf(t, err) != commonv1.ErrorReason_ERROR_REASON_BUCKET_EXISTS_ON_BACKEND {
			t.Fatalf("err = %v, want the bucket refused as existing", err)
		}
	})
	t.Run("a probe's scratch bucket", func(t *testing.T) {
		repo := &fakeRepo{backendEnabled: true, getErr: admindomain.ErrNotFound}
		h := ownershipHandler(repo, okProvisioner{})
		b := admindomain.Bucket{BackendID: "primary", BucketName: probePrefix + "x"}
		_, _, err := h.EnsureBucket(ctxAs(apiutil.RoleBucketAdmin),
			CreateBucketInput{Bucket: b, ProvisionOnBackend: true})
		if reasonOf(t, err) != commonv1.ErrorReason_ERROR_REASON_BUCKET_RESERVED {
			t.Fatalf("err = %v, want the bucket refused as reserved", err)
		}
	})
}

// Deleting on the backend a bucket Paladin adopted would destroy data it
// never wrote; dropping the row stays allowed.
func TestDeleteOnBackendOnlyWhatPaladinCreated(t *testing.T) {
	adopted := admindomain.Bucket{BackendID: "primary", BucketName: "acme"}
	h := ownershipHandler(&fakeRepo{getBucket: adopted}, okProvisioner{onBackend: true})
	err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme", DeleteOnBackend: true})
	if reasonOf(t, err) != commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_CREATED_BY_PALADIN {
		t.Fatalf("err = %v, want the delete on the backend refused", err)
	}
	if err := h.DeleteBucket(ctxAs(apiutil.RoleBucketAdmin),
		DeleteBucketInput{BackendID: "primary", BucketName: "acme"}); err != nil {
		t.Fatalf("dropping the row refused: %v", err)
	}
}
