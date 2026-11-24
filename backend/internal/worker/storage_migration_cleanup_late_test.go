package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The ETags the late-write cleanup tests give the source: what the copy read,
// and a write made after it.
const (
	copiedETag = "copied"
	laterETag  = "later"
)

// A source copy that holds a write the copy did not take is kept for its late
// storage event, and the migration stays completed: deleting it would lose
// the only copy of that write. Only a live object whose last recorded write
// predates the rebind is compared; anything else is cleaned as before.
func TestCleanupKeepsASourceWriteTheCopyDidNotTake(t *testing.T) {
	const key = "docs/a.bin"
	rebound := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	before, after := rebound.Add(-time.Minute), rebound.Add(time.Minute)
	cases := []struct {
		name       string
		sourceETag string
		recorded   *string
		writtenAt  time.Time
		trashed    bool
		identity   bool
		wantKept   bool
	}{
		{"the source holds a later write", laterETag, str(copiedETag), before, false, true, true},
		{"never written since migration 111", laterETag, str(copiedETag), time.Time{}, false, true, true},
		{"the source holds what was copied", copiedETag, str(copiedETag), before, false, true, false},
		{"written in the target since the rebind", laterETag, str(copiedETag), after, false, true, false},
		{"written at the rebind", laterETag, str(copiedETag), rebound, false, true, false},
		{"in the trash", laterETag, str(copiedETag), before, true, true, false},
		{"no record of the copy", laterETag, nil, before, false, true, false},
		{"no identity wired", laterETag, str(copiedETag), before, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := completedMigration(trashTarget)
			repo.mig.ReboundAt = rebound
			repo.objects[0].SourceETag = tc.recorded
			repo.objects[0].WrittenAt = tc.writtenAt
			repo.objects[0].Trashed = tc.trashed
			s := &etagHeader{bucketStore: newBucketStore(), etags: map[string]string{trashSource + "|" + key: tc.sourceETag}}
			s.sizes[trashSource][key], s.sizes[trashTarget][key] = cleanupSize, cleanupSize
			w := cleanupWorker(repo, s.bucketStore)
			if tc.identity {
				w.Identity = s
			}

			err := w.tick(context.Background())
			_, inSource := s.sizes[trashSource][key]
			if tc.wantKept {
				if !errors.Is(err, ErrCleanupKept) {
					t.Fatalf("tick = %v, want ErrCleanupKept", err)
				}
				if !inSource || repo.mig.State != MigStateCompleted {
					t.Fatalf("source kept = %v, state %s; want the copy kept and the migration completed", inSource, repo.mig.State)
				}
				return
			}
			if err != nil {
				t.Fatalf("tick = %v", err)
			}
			if inSource || repo.mig.State != MigStateCleaned {
				t.Fatalf("source kept = %v, state %s; want it deleted and the migration cleaned", inSource, repo.mig.State)
			}
		})
	}
}

// The copy records the ETag the source held just before it, read before the
// copy so a write racing it shows as one the target may lack.
func TestCopyRecordsTheSourceETagItTook(t *testing.T) {
	const key = "docs/a.bin"
	s := &etagHeader{bucketStore: newBucketStore(), etags: map[string]string{trashSource + "|" + key: copiedETag}}
	s.sizes[trashSource][key] = cleanupSize
	repo, o := completedMigration(trashTarget)
	w := &StorageMigrationWorker{Repo: repo, Copier: s, Header: s, Identity: s}
	got, err := w.copyOne(context.Background(), repo.mig, o)
	if err != nil {
		t.Fatalf("copyOne: %v", err)
	}
	if got == nil || *got != copiedETag {
		t.Fatalf("copyOne recorded %v, want %q", got, copiedETag)
	}
	w.Identity = nil
	if got, err := w.copyOne(context.Background(), repo.mig, o); err != nil || got != nil {
		t.Fatalf("copyOne without an Identity = %v, %v; want nothing recorded", got, err)
	}
}
