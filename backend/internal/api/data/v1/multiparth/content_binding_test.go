package multiparth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// fakeSM is a StateMachine whose promote answer the test chooses.
type fakeSM struct {
	promoteErr   error
	failedID     uuid.UUID
	failedReason string
}

func (f *fakeSM) PromoteToAvailable(context.Context, uuid.UUID, string, int64, string, string, statemachine.Source) (bool, error) {
	return f.promoteErr == nil, f.promoteErr
}

func (f *fakeSM) MarkFailed(_ context.Context, id uuid.UUID, reason string) error {
	f.failedID, f.failedReason = id, reason
	return nil
}

func sessionRepo(tid uuid.UUID) *fakeRepo {
	return &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) { return sessionForTenant(tid), nil }}
}

// A part URL was signed for its part number alone, so it accepted a body of
// any length and content. It is now bound to the part's exact length and to
// the checksum the client sends for it.
func TestPresignPartBindsLengthAndChecksum(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		name string
		part int32
		want int64
	}{
		{"a full part", 1, testPartSize},
		{"the last part carries the remainder", testTotalParts, testLastPart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := &fakeStorage{}
			if _, _, _, err := newHandler(sessionRepo(tid), storage, allow()).
				PresignPart(authedCtx(tid), "up-1", tc.part, 0, testPartChecksum, SessionRef{}); err != nil {
				t.Fatal(err)
			}
			got := storage.lastPresign.part
			if got.SizeBytes != tc.want || got.ChecksumValue != testPartChecksum || got.ChecksumAlgo != checksum.SHA256 || got.Number != tc.part {
				t.Fatalf("part binding = %+v, want %d bytes under SHA256", got, tc.want)
			}
		})
	}
}

func TestPresignPartRefusals(t *testing.T) {
	tid := uuid.New()
	t.Run("a malformed checksum", func(t *testing.T) {
		storage := &fakeStorage{}
		_, _, _, err := newHandler(sessionRepo(tid), storage, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, 0, "not-base64!", SessionRef{})
		wantCode(t, err, connect.CodeInvalidArgument)
		if storage.lastPresign.part.Number != 0 {
			t.Fatal("signed with a malformed checksum")
		}
	})
	t.Run("a session from before the binding", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			s := sessionForTenant(tid)
			s.SizeBytes, s.ChecksumAlgo = 0, ""
			return s, nil
		}}
		_, _, _, err := newHandler(repo, &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, 0, testPartChecksum, SessionRef{})
		wantCode(t, err, connect.CodeFailedPrecondition)
	})
}

func TestInitiatePassesTheChecksumAlgorithmToStorage(t *testing.T) {
	storage := &fakeStorage{}
	args := InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", SizeHint: 1024, ChecksumAlgo: checksum.CRC32C}
	if _, err := newHandler(&fakeRepo{}, storage, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args); err != nil {
		t.Fatal(err)
	}
	if storage.lastInitiate.checksumAlgo != checksum.CRC32C {
		t.Fatalf("storage opened the upload with %q, want CRC32C", storage.lastInitiate.checksumAlgo)
	}
}

// A completion list that was not exactly the planned parts was passed to the
// store, which assembles whatever it is given; a subset made an object
// shorter than the one admitted.
func TestCompleteRefusesAnIncompletePartList(t *testing.T) {
	tid := uuid.New()
	cases := map[string]func([]PartETag) []PartETag{
		"a missing part":          func(p []PartETag) []PartETag { return p[:len(p)-1] },
		"a part listed twice":     func(p []PartETag) []PartETag { p[1].PartNumber = 1; return p },
		"a part out of range":     func(p []PartETag) []PartETag { p[0].PartNumber = testTotalParts + 1; return p },
		"a part with no checksum": func(p []PartETag) []PartETag { p[2].ChecksumValue = ""; return p },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			storage := &fakeStorage{}
			_, err := newHandler(sessionRepo(tid), storage, allow()).
				CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: mutate(completeParts())})
			wantCode(t, err, connect.CodeInvalidArgument)
			if storage.lastComplete.storageUploadID != "" {
				t.Fatal("an incomplete list reached the store")
			}
		})
	}
}

func TestCompleteForwardsPartChecksums(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	h := NewHandler(sessionRepo(tid), storage, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
	if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
		t.Fatal(err)
	}
	if storage.lastComplete.checksumAlgo != checksum.SHA256 || storage.lastComplete.parts[0].ChecksumValue != testPartChecksum {
		t.Fatalf("completion sent algo=%q parts=%+v", storage.lastComplete.checksumAlgo, storage.lastComplete.parts)
	}
}

// An assembled object that is not the size it was registered with is
// deleted and its row failed, not promoted.
func TestCompleteDiscardsAMismatchedObject(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &fakeSM{promoteErr: &statemachine.ContentMismatchError{Field: "size", Want: "10", Got: "11"}}
	repo := sessionRepo(tid)
	h := NewHandler(repo, storage, allow(), sm, testTTLPolicy(), testUploadLimits)

	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !errors.Is(err, statemachine.ErrContentMismatch) {
		t.Fatalf("err = %v, want the mismatch named", err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != "photos/cat.jpg" {
		t.Fatalf("deleted %v, want the assembled object", storage.deleted)
	}
	if sm.failedReason != statemachine.FailedContentMismatch {
		t.Fatalf("row failed with %q, want %q", sm.failedReason, statemachine.FailedContentMismatch)
	}
}

// If the bytes cannot be deleted the row is left PENDING, so the reconciler
// retries — a FAILED row over live bytes would leave them forever.
func TestCompleteKeepsTheRowPendingWhenTheDeleteFails(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{deleteErr: errors.New("s3 down")}
	sm := &fakeSM{promoteErr: &statemachine.ContentMismatchError{Field: "size"}}
	h := NewHandler(sessionRepo(tid), storage, allow(), sm, testTTLPolicy(), testUploadLimits)

	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	wantCode(t, err, connect.CodeInternal)
	if sm.failedReason != "" {
		t.Fatal("the row was failed although its bytes are still stored")
	}
}

func TestSessionPartLength(t *testing.T) {
	s := sessionForTenant(uuid.New())
	var total int64
	for n := int32(1); n <= s.TotalParts; n++ {
		total += s.PartLength(n)
	}
	if total != s.SizeBytes {
		t.Fatalf("parts sum to %d, the object is %d", total, s.SizeBytes)
	}
}

// Completion answered with the object's name alone, so every client read the
// object back with GetObject. It answers with the object as stored now.
func TestCompleteReturnsTheStoredObject(t *testing.T) {
	tid := uuid.New()
	repo := sessionRepo(tid)
	repo.object = objecth.Object{Collection: "docs", Key: "big.bin", SizeBytes: 1 << 20}
	h := NewHandler(repo, &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
	got, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Collection != "docs" || got.Key != "big.bin" || got.SizeBytes != 1<<20 {
		t.Errorf("returned %+v, want the stored object", got)
	}
}

// A read that fails after the completion succeeded is not an error: the
// upload is done, and a caller told otherwise would retry a session that no
// longer exists.
func TestCompleteSucceedsWhenTheReadBackFails(t *testing.T) {
	tid := uuid.New()
	repo := sessionRepo(tid)
	repo.objectErr = errors.New("read failed")
	h := NewHandler(repo, &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
	got, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	if err != nil {
		t.Fatalf("a completed upload reported %v", err)
	}
	if got.Collection != "" {
		t.Errorf("returned %+v, want the zero object", got)
	}
}
