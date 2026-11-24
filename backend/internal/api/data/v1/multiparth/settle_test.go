package multiparth

import (
	"context"
	"slices"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// placingSM records where each promote was told the bytes are, and answers
// with the next of errs (nil once they run out).
type placingSM struct {
	fakeSM
	placed []statemachine.Placement
	etags  []string
	errs   []error
}

func (p *placingSM) PromoteToAvailable(_ context.Context, _ uuid.UUID, placed *statemachine.Placement, etag string, _ int64, _, _ string, _ statemachine.Source) (bool, error) {
	p.placed = append(p.placed, *placed)
	p.etags = append(p.etags, etag)
	if len(p.errs) == 0 {
		return true, nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return err == nil, err
}

// reboundRepo serves the test session with its collection bound to another
// bucket than the one the session was anchored to: a storage migration, or
// an operator, moved it since the upload was initiated.
func reboundRepo(tid uuid.UUID) *fakeRepo {
	repo := sessionRepo(tid)
	repo.lookupBucketFn = func(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
		return "backend-dedicated", "bucket-dedicated", nil
	}
	return repo
}

// A multipart upload is assembled and promoted in the bucket its session was
// anchored to, where its parts are (ADR-0051), whatever the collection is
// bound to by then: nothing is copied, and the object is placed there.
func TestCompletePromotesWhereThePartsAre(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{}
	h := NewHandler(reboundRepo(tid), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if storage.lastComplete.bucket != testSessionBucket {
		t.Errorf("assembled in %s, want the session's bucket", storage.lastComplete.bucket)
	}
	if len(storage.copies) != 0 {
		t.Errorf("copies %v, want none", storage.copies)
	}
	if want := []statemachine.Placement{{BackendID: testSessionBackend, Bucket: testSessionBucket}}; !slices.Equal(sm.placed, want) {
		t.Errorf("promoted in %v, want %v", sm.placed, want)
	}
}

// An assembled object that breaks its registration is deleted from the
// bucket it was assembled in before its row is discarded.
func TestCompleteDiscardsAMismatchWhereItWasAssembled(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{errs: []error{&statemachine.ContentMismatchError{Field: "size", Want: "10", Got: "11"}}}
	h := NewHandler(reboundRepo(tid), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if want := []string{testSessionBucket}; !slices.Equal(storage.deletedIn, want) {
		t.Errorf("deleted in %v, want %v", storage.deletedIn, want)
	}
	if sm.failedReason != statemachine.FailedContentMismatch {
		t.Errorf("row failed with %q, want %q", sm.failedReason, statemachine.FailedContentMismatch)
	}
}
