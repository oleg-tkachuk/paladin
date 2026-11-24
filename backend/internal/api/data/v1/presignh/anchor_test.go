package presignh

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// anchoredRepo is pendingRepo with the row's upload anchored to
// backend-old/bucket-old while its collection is bound to backend-1/bucket-1.
func anchoredRepo() *fakeRepo {
	repo := pendingRepo()
	inner := repo.lookupObjectFn
	repo.lookupObjectFn = func(ctx context.Context, tid uuid.UUID, c string, id uuid.UUID) (ObjectRef, error) {
		ref, err := inner(ctx, tid, c, id)
		ref.Upload = &objecth.BucketRef{BackendID: "backend-old", Bucket: "bucket-old",
			State: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}}
		return ref, err
	}
	return repo
}

// A regenerated upload URL is signed for the bucket the row's first URL was
// (ADR-0051), so a row has URLs in one bucket only; while a storage
// migration empties that bucket the URL expires by its write deadline, and
// past it none is signed.
func TestRegenerateSignsForTheAnchorWithinItsDeadline(t *testing.T) {
	tid := uuid.New()
	cfg := testConfig(t)
	t.Run("the anchor, not the binding", func(t *testing.T) {
		st := &fakeStorage{}
		if _, err := NewHandler(anchoredRepo(), st, allowPolicy(), cfg).RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0); err != nil {
			t.Fatal(err)
		}
		if st.gotPut.BackendID != "backend-old" || st.gotPut.Bucket != "bucket-old" {
			t.Fatalf("signed for %s/%s, want the anchor backend-old/bucket-old", st.gotPut.BackendID, st.gotPut.Bucket)
		}
	})
	t.Run("cut to the deadline", func(t *testing.T) {
		const left = 2 * time.Minute
		repo := anchoredRepo()
		repo.writesCloseAt = time.Now().Add(left)
		st := &fakeStorage{}
		if _, err := NewHandler(repo, st, allowPolicy(), cfg).RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0); err != nil {
			t.Fatal(err)
		}
		if st.gotTTL > left || st.gotTTL <= 0 {
			t.Fatalf("signed for %v, want at most the %v left before the deadline", st.gotTTL, left)
		}
	})
	t.Run("past the deadline", func(t *testing.T) {
		repo := anchoredRepo()
		repo.writesCloseAt = time.Now().Add(-time.Minute)
		st := &fakeStorage{}
		_, err := NewHandler(repo, st, allowPolicy(), cfg).RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
		if st.putCalled {
			t.Fatal("signed a URL past the bucket's write deadline")
		}
	})
}
