package multiparth

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

const mib = int64(1 << 20)

// limits.max_multipart_size, the part-size bounds, max_parts and a bucket's
// constraints were configured and stored but never read: the part plan was
// fixed at S3's minimum and nothing refused an upload the operator or the
// bucket owner had ruled out. InitiateMultipartUpload now plans and admits
// through the upload policy.
func TestInitiateHonoursUploadLimits(t *testing.T) {
	tid := uuid.New()
	base := InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", ChecksumAlgo: "SHA256", SizeHint: 100 * mib}

	t.Run("the bucket's minimum part size drives the plan", func(t *testing.T) {
		repo := &fakeRepo{constraints: uploadpolicy.BucketConstraints{MinPartSizeBytes: 16 * mib}}
		_, err := newHandler(repo, &fakeStorage{}, allow()).InitiateMultipartUpload(authedCtx(tid), base)
		if err != nil {
			t.Fatal(err)
		}
		if repo.lastInitiate.args.PartSizeBytes != 16*mib || repo.lastInitiate.args.TotalParts != 7 {
			t.Fatalf("plan = %d bytes × %d parts, want 16 MiB × 7", repo.lastInitiate.args.PartSizeBytes, repo.lastInitiate.args.TotalParts)
		}
	})

	refusals := []struct {
		name        string
		constraints uploadpolicy.BucketConstraints
		mutate      func(*InitiateArgs)
	}{
		{"above the bucket's object size", uploadpolicy.BucketConstraints{MaxObjectSizeBytes: 50 * mib}, nil},
		{"more parts than the bucket allows at its largest part", uploadpolicy.BucketConstraints{MaxPartSizeBytes: 5 * mib, MaxParts: 10}, nil},
		{"a type outside the bucket's allowlist", uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"image/png"}}, nil},
		{"a checksum other than the bucket requires", uploadpolicy.BucketConstraints{RequiredChecksumAlgorithm: "CRC32C"}, nil},
		{"no size", uploadpolicy.BucketConstraints{}, func(a *InitiateArgs) { a.SizeHint = 0 }},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			repo := &fakeRepo{constraints: tc.constraints}
			storage := &fakeStorage{}
			args := base
			if tc.mutate != nil {
				tc.mutate(&args)
			}
			_, err := newHandler(repo, storage, allow()).InitiateMultipartUpload(authedCtx(tid), args)
			wantCode(t, err, connect.CodeInvalidArgument)
			if storage.lastInitiate.backendID != "" {
				t.Fatal("a storage multipart session was opened for a refused upload")
			}
		})
	}

	t.Run("refuses above the global max_multipart_size", func(t *testing.T) {
		limits := testUploadLimits
		limits.MaxMultipartSize = 10 * mib
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allow(), nil, testTTLPolicy(), limits)
		_, err := h.InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeInvalidArgument)
	})
}

// A bucket's max_presign_put_ttl bounds part URLs too: a part URL is a PUT.
func TestPresignPartHonoursBucketTTLCeiling(t *testing.T) {
	tid := uuid.New()
	const ceiling = 5 * time.Minute // below testPartTTL
	repo := &fakeRepo{
		getSessionFn: func(context.Context, string) (Session, error) { return sessionForTenant(tid), nil },
		constraints:  uploadpolicy.BucketConstraints{MaxPresignPutTTL: ceiling},
	}

	storage := &fakeStorage{}
	if _, _, _, err := newHandler(repo, storage, allow()).PresignPart(authedCtx(tid), "up-1", 1, 0, testPartChecksum, SessionRef{}); err != nil {
		t.Fatal(err)
	}
	if storage.lastPresign.ttl != ceiling {
		t.Fatalf("default part ttl = %v, want the bucket ceiling %v", storage.lastPresign.ttl, ceiling)
	}

	storage = &fakeStorage{}
	_, _, _, err := newHandler(repo, storage, allow()).PresignPart(authedCtx(tid), "up-1", 1, ceiling+time.Second, testPartChecksum, SessionRef{})
	wantCode(t, err, connect.CodeInvalidArgument)
	if storage.lastPresign.ttl != 0 {
		t.Fatal("a part URL was signed above the bucket's ceiling")
	}
}
