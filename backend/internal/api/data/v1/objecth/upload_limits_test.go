package objecth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// recordingUploadStorage records what UploadObject asked the presigner for.
type recordingUploadStorage struct {
	noopStorage
	put  *PresignPutArgs
	post *PresignPostArgs
}

func (s *recordingUploadStorage) PresignPut(_ context.Context, a PresignPutArgs) (string, map[string]string, time.Time, error) {
	s.put = &a
	return "https://s3/put", nil, time.Unix(200, 0), nil
}

func (s *recordingUploadStorage) PresignPost(_ context.Context, a PresignPostArgs) (string, map[string]string, time.Time, error) {
	s.post = &a
	return "https://s3/b", map[string]string{}, time.Unix(200, 0), nil
}

func limitsHandler(constraints uploadpolicy.BucketConstraints, limits uploadpolicy.Limits) (*Handler, *uploadRepoRecording, *recordingUploadStorage, context.Context) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	repo := &uploadRepoRecording{uploadRepo: uploadRepo{fakeObjectRepo: fakeObjectRepo{
		meta: BucketMeta{BackendID: "backend-7", BucketName: "bucket-7", Constraints: constraints},
	}}}
	storage := &recordingUploadStorage{}
	cfg := testPresignConfig()
	cfg.Limits = limits
	return &Handler{repo: repo, storage: storage, policy: &recordingAuthorizer{}, presign: cfg}, repo, storage, ctx
}

// uploadRepoRecording notes whether a PENDING row was created.
type uploadRepoRecording struct {
	uploadRepo
	created  bool
	onCreate func(CreateObjectArgs)
}

func (r *uploadRepoRecording) CreateObject(ctx context.Context, args CreateObjectArgs) (Object, error) {
	r.created = true
	if r.onCreate != nil {
		r.onCreate(args)
	}
	return r.uploadRepo.CreateObject(ctx, args)
}

// limits.max_object_size, allowed_content_types and every bucket constraint
// were documented as enforced "at presign time" and read by nothing.
// UploadObject now refuses an upload outside them before a row exists or a
// URL is signed.
func TestUploadObjectEnforcesUploadLimits(t *testing.T) {
	strictGlobal := testUploadLimits
	strictGlobal.MaxObjectSize = 1000
	strictGlobal.AllowedContentTypes = []string{"text/plain", "image/png"}

	cases := []struct {
		name        string
		limits      uploadpolicy.Limits
		constraints uploadpolicy.BucketConstraints
		in          UploadObjectInput
	}{
		{"above limits.max_object_size", strictGlobal, uploadpolicy.BucketConstraints{}, UploadObjectInput{SizeBytes: 1001, ContentType: "text/plain", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}},
		{"a type outside limits.allowed_content_types", strictGlobal, uploadpolicy.BucketConstraints{}, UploadObjectInput{SizeBytes: 1, ContentType: "application/x-msdownload", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}},
		{"above the bucket's max object size", testUploadLimits, uploadpolicy.BucketConstraints{MaxObjectSizeBytes: 10}, UploadObjectInput{SizeBytes: 11, ContentType: "text/plain", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}},
		{"a type outside the bucket's allowlist", testUploadLimits, uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"image/png"}}, UploadObjectInput{SizeBytes: 1, ContentType: "text/plain", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}},
		{"a checksum the bucket does not require", testUploadLimits, uploadpolicy.BucketConstraints{RequiredChecksumAlgorithm: "CRC32C"}, UploadObjectInput{SizeBytes: 1, ContentType: "text/plain", ChecksumAlgo: "SHA256", ChecksumValue: testChecksumValue}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, storage, ctx := limitsHandler(tc.constraints, tc.limits)
			in := tc.in
			in.Collection, in.Key = "docs", "a"
			_, err := h.UploadObject(ctx, in)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if repo.created || storage.put != nil {
				t.Fatal("a refused upload created a row or signed a URL")
			}
		})
	}
}

func TestUploadObjectAdmitsWithinLimits(t *testing.T) {
	h, _, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"text/plain"}}, testUploadLimits)
	if _, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: "a", SizeBytes: 5, ContentType: "text/plain; charset=utf-8", ChecksumAlgo: "SHA256", ChecksumValue: testChecksumValue}); err != nil {
		t.Fatal(err)
	}
	if storage.put == nil {
		t.Fatal("no URL was signed")
	}
}

// The POST policy is bound to the exact size admitted, as a PUT is.
func TestUploadObjectPostIsBoundToTheAdmittedSize(t *testing.T) {
	h, _, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{MaxObjectSizeBytes: 4096}, testUploadLimits)
	if _, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: "a", ContentType: "text/plain", SizeBytes: 4096, TransportPOST: true, ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}); err != nil {
		t.Fatal(err)
	}
	if storage.post == nil || storage.post.SizeBytes != 4096 || storage.post.ChecksumValue != testChecksumValue {
		t.Fatalf("POST args = %+v, want size 4096 and the checksum", storage.post)
	}
}

// The bucket's max_presign_put_ttl shortens the upload URL.
func TestUploadObjectHonoursBucketPutTTLCeiling(t *testing.T) {
	const ceiling = time.Minute
	h, _, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{MaxPresignPutTTL: ceiling}, testUploadLimits)
	if _, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: "a", ContentType: "text/plain", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}); err != nil {
		t.Fatal(err)
	}
	if storage.put == nil || storage.put.TTL != ceiling {
		t.Fatalf("PUT TTL = %+v, want the bucket ceiling %v", storage.put, ceiling)
	}
}

func TestDownloadObjectHonoursBucketGetTTLCeiling(t *testing.T) {
	const ceiling = 2 * time.Minute
	h, storage, _, ctx := downloadHandler(t, statemachine.StateAvailable)
	h.repo.(*downloadRepo).constraints = uploadpolicy.BucketConstraints{MaxPresignGetTTL: ceiling}

	if _, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "", false); err != nil {
		t.Fatal(err)
	}
	if storage.got.TTL != ceiling {
		t.Fatalf("default TTL = %v, want the bucket ceiling %v", storage.got.TTL, ceiling)
	}
	storage.got = PresignGetArgs{}
	_, err := h.DownloadObject(ctx, "docs", "report.pdf", ceiling+time.Second, "", false)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument above the bucket ceiling", connect.CodeOf(err))
	}
	if storage.got.TTL != 0 {
		t.Fatal("signed above the bucket ceiling")
	}
}

// A copy creates an object in the destination bucket; without the check it
// was the way around that bucket's allowlist and size cap.
func TestCopyObjectEnforcesDestinationLimits(t *testing.T) {
	cases := []struct {
		name        string
		constraints uploadpolicy.BucketConstraints
	}{
		{"a type the destination refuses", uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"image/png"}}},
		{"larger than the destination accepts", uploadpolicy.BucketConstraints{MaxObjectSizeBytes: 32}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &completeRepo{obj: availableSource(), constraints: tc.constraints, createErr: errors.New("must not be reached")}
			h, _, ctx, _ := completeHandler(repo, headStorage{})
			_, err := h.CopyObject(ctx, CopyObjectInput{SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst"})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if repo.created != nil {
				t.Fatal("a destination row was created for a refused copy")
			}
		})
	}
}
