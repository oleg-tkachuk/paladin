package objecth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// The upload URL was signed for a Content-Type alone: size_hint and the
// checksum were accepted and dropped, so the URL took any body. It is now
// bound to the admitted size and the declared checksum, and the object is
// registered with both.
func TestUploadObjectBindsSizeAndChecksum(t *testing.T) {
	h, repo, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
	created := &CreateObjectArgs{}
	repo.onCreate = func(a CreateObjectArgs) { *created = a }

	in := UploadObjectInput{Collection: "docs", Key: "a", ContentType: "text/plain", SizeBytes: 1234, ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}
	if _, err := h.UploadObject(ctx, in); err != nil {
		t.Fatal(err)
	}
	if storage.put == nil || storage.put.SizeBytes != 1234 || storage.put.ChecksumValue != testChecksumValue || storage.put.ChecksumAlgo != checksum.SHA256 {
		t.Fatalf("signed %+v, want the size and checksum", storage.put)
	}
	if created.SizeBytes == nil || *created.SizeBytes != 1234 || created.ChecksumValue != testChecksumValue {
		t.Fatalf("registered %+v, want the size and checksum", created)
	}
}

// An empty object is a size of zero, not an unknown size: it is registered
// and signed as 0 bytes.
func TestUploadObjectRegistersAnEmptyObjectAsZeroBytes(t *testing.T) {
	h, repo, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
	created := &CreateObjectArgs{}
	repo.onCreate = func(a CreateObjectArgs) { *created = a }
	if _, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: "e", ContentType: "text/plain", ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue}); err != nil {
		t.Fatal(err)
	}
	if created.SizeBytes == nil || *created.SizeBytes != 0 || storage.put.SizeBytes != 0 {
		t.Fatalf("registered %v, signed %d; want 0 for both", created.SizeBytes, storage.put.SizeBytes)
	}
}

func TestUploadObjectRefusesAMalformedChecksum(t *testing.T) {
	for name, in := range map[string]UploadObjectInput{
		"missing":              {ChecksumAlgo: checksum.SHA256},
		"hex, not base64":      {ChecksumAlgo: checksum.SHA256, ChecksumValue: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		"wrong length for alg": {ChecksumAlgo: checksum.CRC32C, ChecksumValue: testChecksumValue},
	} {
		t.Run(name, func(t *testing.T) {
			h, repo, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
			in.Collection, in.Key, in.ContentType = "docs", "a", "text/plain"
			_, err := h.UploadObject(ctx, in)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if repo.created || storage.put != nil {
				t.Fatal("a row or URL exists for a refused checksum")
			}
		})
	}
}

// deletingStorage is headStorage that records deletes.
type deletingStorage struct {
	headStorage
	deleted   []string
	deleteErr error
}

func (s *deletingStorage) DeleteObject(_ context.Context, _, _ string, _ uuid.UUID, collection, key string) error {
	s.deleted = append(s.deleted, collection+"/"+key)
	return s.deleteErr
}

func registeredPendingObject() Object {
	return Object{
		ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", Key: "a.txt",
		State: statemachine.StatePending, ChecksumAlgo: checksum.SHA256, Checksum: testChecksumValue,
	}
}

// A promote refused because the stored bytes broke the registration used to
// surface as Internal and leave the bytes; they are now deleted, the row is
// failed, and the caller is told what did not match.
func TestCompleteObjectDiscardsMismatchedBytes(t *testing.T) {
	repo := &completeRepo{obj: registeredPendingObject()}
	sm := &fakeStateMachine{promoteErr: &statemachine.ContentMismatchError{Field: "size", Want: "10", Got: "11"}}
	st := &deletingStorage{headStorage: headStorage{etag: "e", size: 11}}
	h, _, _, _, ctx, _ := tailHandler(t, repo, st, sm)

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: repo.obj.ObjectID.String()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, statemachine.ErrContentMismatch) {
		t.Fatalf("err = %v, want FailedPrecondition naming the mismatch", err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "docs/a.txt" {
		t.Fatalf("deleted %v, want the object's bytes", st.deleted)
	}
	if len(sm.markFailedBy) != 1 || sm.markFailedBy[0] != statemachine.FailedContentMismatch {
		t.Fatalf("failed with %v, want %q", sm.markFailedBy, statemachine.FailedContentMismatch)
	}
}

func TestCompleteObjectLeavesTheRowPendingWhenTheDeleteFails(t *testing.T) {
	repo := &completeRepo{obj: registeredPendingObject()}
	sm := &fakeStateMachine{promoteErr: &statemachine.ContentMismatchError{Field: "checksum"}}
	st := &deletingStorage{headStorage: headStorage{etag: "e", size: 1}, deleteErr: errors.New("s3 down")}
	h, _, _, _, ctx, _ := tailHandler(t, repo, st, sm)

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: repo.obj.ObjectID.String()})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if len(sm.markedFailed) != 0 {
		t.Fatal("the row was failed while its bytes are still stored")
	}
}

// The caller's checksum was declared on the request and never read.
func TestCompleteObjectRefusesADifferentCallerChecksum(t *testing.T) {
	repo := &completeRepo{obj: registeredPendingObject()}
	sm := &fakeStateMachine{changed: true}
	h, _, _, _, ctx, _ := tailHandler(t, repo, headStorage{etag: "e", size: 1}, sm)

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: repo.obj.ObjectID.String(), Checksum: "AAAAAA=="})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if sm.promoteCalls != 0 {
		t.Fatal("promoted despite the caller's checksum disagreeing")
	}
}

// headAlgoStorage records the algorithm CompleteObject asked HEAD for.
type headAlgoStorage struct {
	headStorage
	algo *string
}

func (s headAlgoStorage) Head(_ context.Context, _, _ string, _ uuid.UUID, _, _, algo string) (string, int64, string, string, error) {
	*s.algo = algo
	return "e", 1, "", "", nil
}

func TestCompleteObjectHeadsUnderTheObjectsAlgorithm(t *testing.T) {
	repo := &completeRepo{obj: registeredPendingObject()}
	repo.obj.ChecksumAlgo = checksum.CRC32C
	var algo string
	h, _, _, _, ctx, _ := tailHandler(t, repo, headAlgoStorage{algo: &algo}, &fakeStateMachine{changed: true})
	if _, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: repo.obj.ObjectID.String()}); err != nil {
		t.Fatal(err)
	}
	if algo != checksum.CRC32C {
		t.Fatalf("HEAD asked for %q, want the object's CRC32C", algo)
	}
}

func TestDownloadObjectETagBinding(t *testing.T) {
	h, storage, _, ctx := downloadHandler(t, statemachine.StateAvailable)
	h.repo.(*downloadRepo).etag = "etag-9"
	if _, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "", true); err != nil {
		t.Fatal(err)
	}
	if storage.got.IfMatch != "etag-9" {
		t.Fatalf("If-Match = %q, want the object's ETag", storage.got.IfMatch)
	}

	h, _, _, ctx = downloadHandler(t, statemachine.StateAvailable)
	_, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "", true)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition for an object with no ETag", connect.CodeOf(err))
	}
}
