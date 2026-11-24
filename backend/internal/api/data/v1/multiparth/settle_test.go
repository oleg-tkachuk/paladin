package multiparth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The bucket a storage migration rebound the session's collection to.
const (
	reboundBackend = "backend-dedicated"
	reboundBucket  = "bucket-dedicated"
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

// reboundRepo serves the test session, its collection bound to each of
// bindings in turn (the last one from then on).
func reboundRepo(tid uuid.UUID, bindings ...[2]string) *fakeRepo {
	repo := sessionRepo(tid)
	repo.lookupBucketFn = func(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
		b := bindings[0]
		if len(bindings) > 1 {
			bindings = bindings[1:]
		}
		return b[0], b[1], nil
	}
	return repo
}

var (
	anchorBinding  = [2]string{testSessionBackend, testSessionBucket}
	reboundBinding = [2]string{reboundBackend, reboundBucket}
	movedErr       = &statemachine.BindingMovedError{}
)

// A multipart upload completed after a storage migration rebound its
// collection used to be promoted in the bucket its session was anchored to:
// read from the new one, where it is not, and lost once the migration's
// cleanup deleted the anchored copy. It is now copied into the collection's
// bucket, checked, and promoted there, under the ETag that copy reports.
func TestCompleteMovesAnUploadAssembledBeforeTheRebind(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{}
	h := NewHandler(reboundRepo(tid, reboundBinding), storage, allow(), sm, testTTLPolicy(), testUploadLimits)

	if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if storage.lastComplete.bucket != testSessionBucket {
		t.Errorf("assembled in %s, want the session's bucket", storage.lastComplete.bucket)
	}
	if want := []string{testSessionBucket + ">" + reboundBucket}; !slices.Equal(storage.copies, want) {
		t.Errorf("copies %v, want %v", storage.copies, want)
	}
	if want := []statemachine.Placement{{BackendID: reboundBackend, Bucket: reboundBucket}}; !slices.Equal(sm.placed, want) {
		t.Errorf("promoted in %v, want %v", sm.placed, want)
	}
	if !slices.Equal(sm.etags, []string{fakeCopiedETag}) {
		t.Errorf("promoted with ETag %v, want the copy's", sm.etags)
	}
	if len(storage.deleted) != 0 {
		t.Errorf("deleted %v; the anchored copy is the cleanup's to delete", storage.deleted)
	}
}

// A collection still bound where the session was anchored needs no move.
func TestCompleteInTheAnchoredBucketCopiesNothing(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{}
	h := NewHandler(reboundRepo(tid, anchorBinding), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(storage.copies) != 0 || len(storage.heads) != 0 {
		t.Errorf("copies %v, heads %v; want none", storage.copies, storage.heads)
	}
	if want := []statemachine.Placement{{BackendID: testSessionBackend, Bucket: testSessionBucket}}; !slices.Equal(sm.placed, want) {
		t.Errorf("promoted in %v, want %v", sm.placed, want)
	}
}

// The promote is refused when the collection moves between the lookup and
// the promote; the completion follows it, since the storage upload is
// consumed and a client's retry could not assemble the parts again.
func TestCompleteFollowsACollectionRebound(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{errs: []error{movedErr}}
	h := NewHandler(reboundRepo(tid, anchorBinding, reboundBinding), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	want := []statemachine.Placement{{BackendID: testSessionBackend, Bucket: testSessionBucket}, {BackendID: reboundBackend, Bucket: reboundBucket}}
	if !slices.Equal(sm.placed, want) {
		t.Errorf("promoted in %v, want %v", sm.placed, want)
	}
	if !slices.Equal(storage.copies, []string{testSessionBucket + ">" + reboundBucket}) {
		t.Errorf("copies %v, want the move after the refusal", storage.copies)
	}
}

// A collection that keeps moving is not chased for ever: the object stays
// PENDING, anchored, for the reconciler.
func TestCompleteStopsFollowingAfterTheLastMove(t *testing.T) {
	tid := uuid.New()
	sm := &placingSM{errs: slices.Repeat([]error{movedErr}, maxBindingMoves+1)}
	h := NewHandler(reboundRepo(tid, reboundBinding), &fakeStorage{}, allow(), sm, testTTLPolicy(), testUploadLimits)
	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	wantCode(t, err, connect.CodeUnavailable)
	if len(sm.placed) != maxBindingMoves+1 {
		t.Errorf("%d promotes, want %d", len(sm.placed), maxBindingMoves+1)
	}
}

// A move that fails, or whose copy is not the assembled size, promotes
// nothing: the object would be read from a bucket that does not hold it.
func TestCompleteRefusesAMoveThatDidNotLand(t *testing.T) {
	for name, storage := range map[string]*fakeStorage{
		"the copy fails": {copyErr: errors.New("copy: 503")},
		"the copy is short": {headFn: func(string) (string, int64, string, string, error) {
			return fakeCopiedETag, testCompletedSize - 1, "", "", nil
		}},
		"the copy cannot be read": {headFn: func(string) (string, int64, string, string, error) {
			return "", 0, "", "", errors.New("head: 500")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			tid := uuid.New()
			sm := &placingSM{}
			h := NewHandler(reboundRepo(tid, reboundBinding), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
			_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
			wantCode(t, err, connect.CodeUnavailable)
			if len(sm.placed) != 0 {
				t.Errorf("promoted in %v after a move that did not land", sm.placed)
			}
		})
	}
}

// A moved object that breaks its registration is deleted from both buckets
// it is in before its row is discarded.
func TestCompleteDiscardsAMovedMismatchEverywhere(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{}
	sm := &placingSM{errs: []error{&statemachine.ContentMismatchError{Field: "size", Want: "10", Got: "11"}}}
	h := NewHandler(reboundRepo(tid, reboundBinding), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if want := []string{testSessionBucket, reboundBucket}; !slices.Equal(storage.deletedIn, want) {
		t.Errorf("deleted in %v, want %v", storage.deletedIn, want)
	}
	if sm.failedReason != statemachine.FailedContentMismatch {
		t.Errorf("row failed with %q, want %q", sm.failedReason, statemachine.FailedContentMismatch)
	}
}

// A collection's bucket the gate refuses a write to is not written: the
// object stays PENDING in the anchored bucket.
func TestCompleteDoesNotMoveIntoARefusedBucket(t *testing.T) {
	tid := uuid.New()
	repo := sessionRepo(tid)
	repo.lookupBucketFn = func(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
		return "", "", bucketgate.ErrBackendReadOnly
	}
	storage := &fakeStorage{}
	sm := &placingSM{}
	h := NewHandler(repo, storage, allow(), sm, testTTLPolicy(), testUploadLimits)
	_, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()})
	if err == nil {
		t.Fatal("completed into a bucket the gate refuses")
	}
	if len(storage.copies) != 0 || len(sm.placed) != 0 {
		t.Errorf("copies %v, promotes %v; want none", storage.copies, sm.placed)
	}
}
