package worker

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"
)

// cleanupSize is the recorded size of the object the cleanup tests clean.
const cleanupSize = int64(7)

// completedMigration is a migration past its retention window, with one
// object at cleanupSize whose collection is bound to bound.
func completedMigration(bound string) (*fakeMigRepo, ObjectRef) {
	o := ObjectRef{Collection: "docs", Key: "a.bin", SizeBytes: cleanupSize, BoundBackendID: "primary", BoundBucket: bound}
	return &fakeMigRepo{
		mig: StorageMigration{
			TenantID: uuid.New(), State: MigStateCompleted,
			SourceBackendID: "primary", SourceBucketName: trashSource,
			TargetBackendID: "primary", TargetBucketName: trashTarget,
			CleanupAfter: time.Now().Add(-time.Minute),
		},
		objects: []ObjectRef{o},
	}, o
}

func cleanupWorker(repo *fakeMigRepo, s *bucketStore) *StorageMigrationWorker {
	return &StorageMigrationWorker{Repo: repo, Copier: s, Deleter: s, Header: s, CopyBatch: 10, Now: time.Now}
}

// The cleanup deletes a source copy only once the target holds the object at
// its recorded size, so it never deletes an object's only copy: an object
// promoted after the rebind with its bytes left in the source used to be lost
// here. Such a copy is carried to the target first; one neither bucket holds
// as recorded is kept, and the migration stays completed until it is not.
func TestCleanupNeverDeletesTheOnlyCopy(t *testing.T) {
	const key = "docs/a.bin"
	cases := []struct {
		name         string
		bound        string
		source       map[string]int64
		target       map[string]int64
		wantState    string
		wantInSource bool
		wantTarget   int64 // 0: absent
	}{
		{"the target holds it", trashTarget, map[string]int64{key: cleanupSize}, map[string]int64{key: cleanupSize}, MigStateCleaned, false, cleanupSize},
		{"only the source holds it", trashTarget, map[string]int64{key: cleanupSize}, map[string]int64{}, MigStateCleaned, false, cleanupSize},
		{"the target holds a stale size", trashTarget, map[string]int64{key: cleanupSize}, map[string]int64{key: cleanupSize - 1}, MigStateCleaned, false, cleanupSize},
		{"neither holds it as recorded", trashTarget, map[string]int64{key: cleanupSize + 1}, map[string]int64{}, MigStateCompleted, true, 0},
		{"neither holds it at all", trashTarget, map[string]int64{}, map[string]int64{}, MigStateCleaned, false, 0},
		{"the collection stayed on the source", trashSource, map[string]int64{key: cleanupSize}, map[string]int64{}, MigStateCleaned, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := completedMigration(tc.bound)
			s := newBucketStore()
			s.sizes[trashSource], s.sizes[trashTarget] = tc.source, tc.target
			err := cleanupWorker(repo, s).tick(context.Background())
			if tc.wantState == MigStateCompleted && !errors.Is(err, ErrCleanupKept) {
				t.Fatalf("tick = %v, want ErrCleanupKept", err)
			}
			if tc.wantState == MigStateCleaned && err != nil {
				t.Fatalf("tick = %v", err)
			}
			if repo.mig.State != tc.wantState {
				t.Errorf("state = %s, want %s", repo.mig.State, tc.wantState)
			}
			if _, in := s.sizes[trashSource][key]; in != tc.wantInSource {
				t.Errorf("source holds it = %v, want %v", in, tc.wantInSource)
			}
			if got := s.sizes[trashTarget][key]; got != tc.wantTarget {
				t.Errorf("target holds size %d, want %d", got, tc.wantTarget)
			}
		})
	}
}

// A HEAD the cleanup cannot read deletes nothing: the next tick looks again.
func TestCleanupDeletesNothingItCannotCheck(t *testing.T) {
	repo, _ := completedMigration(trashTarget)
	s := newBucketStore()
	s.sizes[trashSource]["docs/a.bin"] = cleanupSize
	s.headErr[trashTarget] = errors.New("head: 503")
	if err := cleanupWorker(repo, s).tick(context.Background()); err == nil {
		t.Fatal("tick = nil, want the HEAD failure")
	}
	if len(s.deletes) != 0 || repo.mig.State != MigStateCompleted {
		t.Errorf("deleted %v, state %s; want nothing deleted and completed", s.deletes, repo.mig.State)
	}
}
