package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"
)

// Bucket names the trash tests migrate between.
const (
	trashSource = "paladin-shared"
	trashTarget = "paladin-dedicated"
)

// bucketStore is an in-memory pair of buckets: copies move sizes between
// them, HEADs read them. It stands in for the copier, deleter and header at
// once, so what one step writes the next one reads.
type bucketStore struct {
	sizes map[string]map[string]int64 // bucket -> "collection/key" -> size
	// headErr, when set, answers every HEAD of a bucket with it.
	headErr map[string]error
	copies  []string
	deletes []string
}

func newBucketStore() *bucketStore {
	return &bucketStore{
		sizes:   map[string]map[string]int64{trashSource: {}, trashTarget: {}},
		headErr: map[string]error{},
	}
}

func objKey(loc CopyLocation) string { return loc.Collection + "/" + loc.Key }

func (s *bucketStore) CopyObject(_ context.Context, src, dst CopyLocation) error {
	size, ok := s.sizes[src.Bucket][objKey(src)]
	if !ok {
		return fmt.Errorf("copy %s: NoSuchKey", objKey(src))
	}
	s.sizes[dst.Bucket][objKey(dst)] = size
	s.copies = append(s.copies, objKey(src))
	return nil
}

func (s *bucketStore) HeadObject(_ context.Context, loc CopyLocation) (int64, error) {
	if err := s.headErr[loc.Bucket]; err != nil {
		return -1, err
	}
	if size, ok := s.sizes[loc.Bucket][objKey(loc)]; ok {
		return size, nil
	}
	return -1, fmt.Errorf("head %s: %w", objKey(loc), ErrObjectMissing)
}

func (s *bucketStore) DeleteObject(_ context.Context, loc CopyLocation) error {
	delete(s.sizes[loc.Bucket], objKey(loc))
	s.deletes = append(s.deletes, objKey(loc))
	return nil
}

// trashFixture is a tenant with one live object and one trashed object, both
// with bytes in the source bucket.
func trashFixture() (*fakeMigRepo, *bucketStore) {
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: uuid.New(), State: MigStateProvisioning,
			SourceBackendID: "primary", SourceBucketName: trashSource,
			TargetBackendID: "primary", TargetBucketName: trashTarget,
		},
		bucketState: "ready",
		objects: []ObjectRef{
			{Collection: "docs", Key: "live.txt", SizeBytes: 3},
			{Collection: "docs", Key: "trashed.txt", SizeBytes: 5, Trashed: true},
		},
	}
	store := newBucketStore()
	store.sizes[trashSource]["docs/live.txt"] = 3
	store.sizes[trashSource]["docs/trashed.txt"] = 5
	return repo, store
}

func trashWorker(repo *fakeMigRepo, store *bucketStore, now time.Time) *StorageMigrationWorker {
	return &StorageMigrationWorker{
		Repo: repo, Copier: store, Deleter: store, Header: store, CopyBatch: 1,
		Now: func() time.Time { return now },
	}
}

// A migration used to copy, verify and clean up live objects only: a trashed
// object's bytes stayed in the source while its collection moved, so a
// restore after the migration found none. They now move with the collection,
// are verified, and leave the source with the rest at cleanup.
func TestStorageMigration_TrashMovesWithTheTenant(t *testing.T) {
	repo, store := trashFixture()
	w := trashWorker(repo, store, time.Now())
	runToTerminal(t, w, repo)

	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q (%s), want completed", repo.mig.State, repo.reason)
	}
	if got, ok := store.sizes[trashTarget]["docs/trashed.txt"]; !ok || got != 5 {
		t.Fatalf("trashed object in the target: size %d, present %v; want its 5 bytes", got, ok)
	}
	if repo.mig.ObjectsCopied != 2 {
		t.Errorf("objects_copied = %d, want 2", repo.mig.ObjectsCopied)
	}

	// Past the retention window: the trashed object's old copy goes too.
	repo.mig.CleanupAfter = time.Now().Add(-time.Minute)
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("cleanup tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateCleaned {
		t.Fatalf("state = %q, want cleaned", repo.mig.State)
	}
	if left := store.sizes[trashSource]; len(left) != 0 {
		t.Errorf("source still holds %v after cleanup, want nothing", left)
	}
}

// A trashed object whose bytes were deleted straight from the bucket has
// nothing to move: the copy skips it rather than failing on it every tick,
// and the verify accepts it missing from the target.
func TestStorageMigration_TrashedWithoutBytesIsSkipped(t *testing.T) {
	repo, store := trashFixture()
	delete(store.sizes[trashSource], "docs/trashed.txt")
	runToTerminal(t, trashWorker(repo, store, time.Now()), repo)

	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q (%s), want completed", repo.mig.State, repo.reason)
	}
	if fmt.Sprint(store.copies) != "[docs/live.txt]" {
		t.Errorf("copies = %v, want only the live object", store.copies)
	}
	if repo.mig.ObjectsCopied != 2 {
		t.Errorf("objects_copied = %d, want the skipped object counted (2)", repo.mig.ObjectsCopied)
	}
}

// A source that cannot say whether a trashed object has bytes is retried,
// not skipped: skipping on an error would lose bytes that are there.
func TestStorageMigration_TrashedUnreadableSourceRetries(t *testing.T) {
	repo, store := trashFixture()
	store.headErr[trashSource] = errors.New("connection reset")
	w := trashWorker(repo, store, time.Now())
	var tickErr error
	for range 4 {
		tickErr = w.tick(context.Background())
	}
	if tickErr == nil || !errors.Is(tickErr, store.headErr[trashSource]) {
		t.Fatalf("tick = %v, want the source HEAD failure", tickErr)
	}
	if repo.mig.State != MigStateCopying || repo.mig.ObjectsCopied != 1 {
		t.Fatalf("state = %q, copied = %d; want copying, stopped before the trashed object",
			repo.mig.State, repo.mig.ObjectsCopied)
	}
}

// The verify of a trashed object missing from the target: sound only when
// the source has no bytes for it either.
func TestStorageMigration_VerifyTrashedMissing(t *testing.T) {
	for name, tc := range map[string]struct {
		sourceSize *int64 // nil: no bytes in the source
		sourceErr  error
		wantReason string // "" = the verify passes
	}{
		"no bytes anywhere":    {},
		"source still has it":  {sourceSize: new(int64(5)), wantReason: "docs/trashed.txt " + verifyProblemMissing},
		"source cannot answer": {sourceErr: errors.New("timeout"), wantReason: "docs/trashed.txt " + verifyProblemSourceUnreadable},
	} {
		t.Run(name, func(t *testing.T) {
			repo, store := trashFixture()
			repo.mig.State, repo.mig.ObjectsTotal, repo.mig.ObjectsCopied = MigStateVerifying, 2, 2
			store.sizes[trashTarget]["docs/live.txt"] = 3
			delete(store.sizes[trashSource], "docs/trashed.txt")
			if tc.sourceSize != nil {
				store.sizes[trashSource]["docs/trashed.txt"] = *tc.sourceSize
			}
			if tc.sourceErr != nil {
				store.headErr[trashSource] = tc.sourceErr
			}
			if err := trashWorker(repo, store, time.Now()).tick(context.Background()); err != nil {
				t.Fatalf("tick = %v, want nil", err)
			}
			switch {
			case tc.wantReason == "" && repo.mig.State != MigStateCompleted:
				t.Errorf("state = %q (%s), want completed", repo.mig.State, repo.reason)
			case tc.wantReason != "" && (repo.mig.State != MigStateRollingBack || repo.reason != reasonVerifyPrefix+tc.wantReason):
				t.Errorf("state = %q, reason %q; want rolling back with %q", repo.mig.State, repo.reason, reasonVerifyPrefix+tc.wantReason)
			}
		})
	}
}

// A live object missing from the target fails the verify whatever the
// source holds: only a trashed one may have had nothing to move.
func TestStorageMigration_VerifyLiveMissingFails(t *testing.T) {
	repo, store := trashFixture()
	repo.mig.State, repo.mig.ObjectsTotal, repo.mig.ObjectsCopied = MigStateVerifying, 2, 2
	delete(store.sizes[trashSource], "docs/live.txt")
	store.sizes[trashTarget]["docs/trashed.txt"] = 5
	if err := trashWorker(repo, store, time.Now()).tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if want := reasonVerifyPrefix + "docs/live.txt " + verifyProblemMissing; repo.mig.State != MigStateRollingBack || repo.reason != want {
		t.Errorf("state = %q, reason %q; want rolling back with %q", repo.mig.State, repo.reason, want)
	}
}

// Without a Header nothing is confirmed missing, so a trashed object is
// copied like a live one.
func TestStorageMigration_TrashedCopiedWithoutHeader(t *testing.T) {
	repo, store := trashFixture()
	w := trashWorker(repo, store, time.Now())
	w.Header = nil
	runToTerminal(t, w, repo)
	if repo.mig.State != MigStateCompleted || len(store.copies) != 2 {
		t.Fatalf("state = %q, copies %v; want completed with both objects copied", repo.mig.State, store.copies)
	}
}
