package objecth

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The bucket the tests place an object's bytes in, away from every fake's
// binding: a storage migration, or an operator's rebind, has moved the
// collection since the object was written (ADR-0051).
const (
	movedBackend = "backend-old"
	movedBucket  = "bucket-old"
)

// placedAway is obj with its bytes in movedBackend/movedBucket.
func placedAway(obj Object) Object {
	obj.Stored = storedIn(movedBackend, movedBucket)
	return obj
}

// awayDownloadRepo serves downloadRepo's object with its bytes placed away
// from the collection's binding.
type awayDownloadRepo struct{ downloadRepo }

func (r *awayDownloadRepo) FindByName(ctx context.Context, tenantID uuid.UUID, collection, id string) (Object, error) {
	obj, err := r.downloadRepo.FindByName(ctx, tenantID, collection, id)
	return placedAway(obj), err
}

// A download URL is signed for the bucket the bytes are in.
func TestDownloadObjectSignsForThePlacement(t *testing.T) {
	h, storage, _, ctx := downloadHandler(t, statemachine.StateAvailable)
	h.repo = &awayDownloadRepo{downloadRepo{state: statemachine.StateAvailable, bucket: "bucket-7"}}
	if _, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "", false); err != nil {
		t.Fatalf("DownloadObject: %v", err)
	}
	if storage.got.BackendID != movedBackend || storage.got.Bucket != movedBucket {
		t.Fatalf("signed for %s/%s, want %s/%s, where the bytes are", storage.got.BackendID, storage.got.Bucket, movedBackend, movedBucket)
	}
}

// sourceRecordingStorage records where a copy read from.
type sourceRecordingStorage struct {
	recordingCopyStorage
	src *Location
}

func (s *sourceRecordingStorage) CopyObject(ctx context.Context, src, dst Location) error {
	s.src = &src
	return s.recordingCopyStorage.CopyObject(ctx, src, dst)
}

// A copy reads its source where the source's bytes are, and writes the
// destination's binding.
func TestCopyObjectReadsTheSourcesPlacement(t *testing.T) {
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	storage := &sourceRecordingStorage{}
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, storage, sm)
	repo.obj = placedAway(copySource(tenantID))
	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.New().String(), DestCollection: "dst",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if storage.src == nil || storage.src.BackendID != movedBackend || storage.src.Bucket != movedBucket {
		t.Fatalf("copied from %+v, want %s/%s, where the source's bytes are", storage.src, movedBackend, movedBucket)
	}
	if storage.dst == nil || storage.dst.Bucket != "bucket-7" {
		t.Fatalf("copied to %+v, want the destination's binding bucket-7", storage.dst)
	}
}

// headAtStorage records where a HEAD looked.
type headAtStorage struct {
	headStorage
	backendID, bucket *string
}

func (s headAtStorage) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, algo string) (string, int64, string, string, error) {
	*s.backendID, *s.bucket = backendID, bucket
	return s.headStorage.Head(ctx, backendID, bucket, tenantID, collection, key, algo)
}

// A restore checks the bytes where they are.
func TestRestoreChecksTheBytesWhereTheyAre(t *testing.T) {
	var backendID, bucket string
	h := &Handler{repo: &fakeObjectRepo{}, storage: headAtStorage{backendID: &backendID, bucket: &bucket}}
	obj := placedAway(Object{ObjectID: uuid.New(), Collection: "docs", Key: "a.txt", State: statemachine.StateDeleted})
	if err := h.assertBytesPresent(context.Background(), uuid.New(), obj); err != nil {
		t.Fatalf("assertBytesPresent: %v", err)
	}
	if backendID != movedBackend || bucket != movedBucket {
		t.Fatalf("HEAD at %s/%s, want %s/%s", backendID, bucket, movedBackend, movedBucket)
	}
}

// A permanent delete owes, and deletes, the bytes where they are.
func TestPermanentDeleteOwesThePlacement(t *testing.T) {
	tenantID := uuid.New()
	repo := &purgeRepo{obj: placedAway(Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.txt", State: statemachine.StateAvailable})}
	h := newPurgeHandler(t, repo, noopStorage{})
	if err := h.PermanentDelete(purgeCtx(tenantID), tenantID, repo.obj, 0, false); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}
	if len(repo.enqueued) != 1 {
		t.Fatalf("debts = %d, want 1", len(repo.enqueued))
	}
	if d := repo.enqueued[0]; d.BackendID != movedBackend || d.BucketName != movedBucket {
		t.Fatalf("debt on %s/%s, want %s/%s, where the bytes are", d.BackendID, d.BucketName, movedBackend, movedBucket)
	}
}
