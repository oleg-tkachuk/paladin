package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"
)

// The moments the catch-up tests place writes around.
var (
	copyStart = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	rebindAt  = copyStart.Add(time.Hour)
)

// etagHeader is a bucketStore that also reads ETags: each bucket's copy of an
// object carries the content it was put with.
type etagHeader struct {
	*bucketStore
	etags map[string]string // bucket|collection/key -> etag
}

func (h *etagHeader) ObjectETag(_ context.Context, loc CopyLocation) (string, error) {
	if e, ok := h.etags[loc.Bucket+"|"+objKey(loc)]; ok {
		return e, nil
	}
	return "", fmt.Errorf("etag %s: %w", objKey(loc), ErrObjectMissing)
}

func (h *etagHeader) CopyObject(ctx context.Context, src, dst CopyLocation) error {
	if err := h.bucketStore.CopyObject(ctx, src, dst); err != nil {
		return err
	}
	h.etags[dst.Bucket+"|"+objKey(dst)] = h.etags[src.Bucket+"|"+objKey(src)]
	return nil
}

func str(s string) *string { return &s }

// A target copy may predate an object's latest write only when the write
// came after the copy started and before the rebind, and the catch-up did
// not copy that very write. A size check cannot tell: an overwrite of the
// same size passes it.
func TestCatchUpProblem(t *testing.T) {
	during := copyStart.Add(time.Minute)
	cases := []struct {
		name     string
		written  time.Time
		caughtUp *CaughtUp
		problem  bool
	}{
		{"never written since bytes_written_at exists", time.Time{}, nil, false},
		{"written before the copy started", copyStart.Add(-time.Second), nil, false},
		{"written at the rebind or after, into the target", rebindAt, nil, false},
		{"written during the copy and caught up", during, &CaughtUp{WrittenAt: during}, false},
		{"written during the copy, never caught up", during, nil, true},
		{"written during the copy, an older write caught up", during, &CaughtUp{WrittenAt: during.Add(-time.Second)}, true},
		{"written at the copy's start", copyStart, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := catchUpProblem(ObjectRef{WrittenAt: tc.written, CaughtUp: tc.caughtUp}, copyStart, rebindAt)
			if (got != "") != tc.problem {
				t.Fatalf("catchUpProblem = %q, want a problem: %v", got, tc.problem)
			}
		})
	}
}

// rebindingRepo is a migration in rebinding whose objects were written
// behind the copy's cursor.
func rebindingRepo(uncaught ...WrittenObject) *fakeMigRepo {
	return &fakeMigRepo{
		mig: StorageMigration{
			TenantID: uuid.New(), State: MigStateRebinding,
			SourceBackendID: "primary", SourceBucketName: trashSource,
			TargetBackendID: "primary", TargetBucketName: trashTarget,
			CopyStartedAt: copyStart,
		},
		uncaught: uncaught,
	}
}

func writtenBehind(key string, at time.Time) WrittenObject {
	return WrittenObject{ObjectRef: ObjectRef{Collection: "docs", Key: key, SizeBytes: cleanupSize}, ID: uuid.New(), WrittenAt: at}
}

// The rebind waits for the catch-up: a tick that finds objects written since
// the copy started copies them, records the write and the copy's ETag, and
// does not rebind; the next tick, with none left, rebinds.
func TestRebindingCatchesUpBeforeItRebinds(t *testing.T) {
	o := writtenBehind("a.bin", copyStart.Add(time.Minute))
	repo := rebindingRepo(o)
	s := &etagHeader{bucketStore: newBucketStore(), etags: map[string]string{trashSource + "|docs/a.bin": "v2"}}
	s.sizes[trashSource]["docs/a.bin"] = cleanupSize
	w := &StorageMigrationWorker{Repo: repo, Copier: s, Header: s, Identity: s, CopyBatch: 10}

	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if repo.rebound {
		t.Fatal("rebound before the catch-up copied what was written behind the cursor")
	}
	got := repo.caughtUp[o.ID]
	if got == nil || !got.WrittenAt.Equal(o.WrittenAt) || got.ETag == nil || *got.ETag != "v2" {
		t.Fatalf("recorded %+v, want the write copied with the target's ETag v2", got)
	}
	if s.etags[trashTarget+"|docs/a.bin"] != "v2" {
		t.Fatal("the target does not hold the latest write")
	}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !repo.rebound || repo.mig.State != MigStateVerifying {
		t.Fatalf("rebound=%v state=%s, want rebound and verifying", repo.rebound, repo.mig.State)
	}
}

// A write landing between the last catch-up pass and the rebind's check
// refuses the rebind; the migration stays in rebinding and catches it up.
func TestRebindRefusedByAWriteSinceTheLastPass(t *testing.T) {
	repo := rebindingRepo()
	repo.behind = 1
	w := &StorageMigrationWorker{Repo: repo, Copier: newBucketStore(), CopyBatch: 10}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v; a refusal is a deferral, not a failure", err)
	}
	if repo.rebound || repo.mig.State != MigStateRebinding {
		t.Fatalf("rebound=%v state=%s, want neither rebound nor moved on", repo.rebound, repo.mig.State)
	}
}

// A catch-up copy that fails leaves the migration in rebinding for the next
// tick, without rebinding.
func TestCatchUpFailureRetries(t *testing.T) {
	repo := rebindingRepo(writtenBehind("gone.bin", copyStart.Add(time.Minute)))
	w := &StorageMigrationWorker{Repo: repo, Copier: newBucketStore(), CopyBatch: 10}
	if err := w.tick(context.Background()); err == nil {
		t.Fatal("tick = nil, want the copy's failure")
	}
	if repo.rebound || len(repo.caughtUp) != 0 {
		t.Fatalf("rebound=%v recorded=%v after a failed copy", repo.rebound, repo.caughtUp)
	}
}

// The verify compares the target's ETag with the one it reported for the
// catch-up's copy of the latest write: a target replaced since, at the same
// size, fails it.
func TestVerifyIdentity(t *testing.T) {
	during := copyStart.Add(time.Minute)
	for name, tc := range map[string]struct {
		target   string
		caughtUp *CaughtUp
		written  time.Time
		fails    bool
	}{
		"the catch-up's copy":                {"v2", &CaughtUp{WrittenAt: during, ETag: str("v2")}, during, false},
		"replaced since the catch-up":        {"v3", &CaughtUp{WrittenAt: during, ETag: str("v2")}, during, true},
		"copied with no ETag":                {"v3", &CaughtUp{WrittenAt: during}, during, false},
		"never caught up":                    {"v3", nil, copyStart.Add(-time.Minute), false},
		"written again after the catch-up's": {"v4", &CaughtUp{WrittenAt: during, ETag: str("v2")}, rebindAt.Add(time.Second), false},
	} {
		t.Run(name, func(t *testing.T) {
			s := &etagHeader{bucketStore: newBucketStore(), etags: map[string]string{trashTarget + "|docs/a.bin": tc.target}}
			s.sizes[trashTarget]["docs/a.bin"] = cleanupSize
			m := StorageMigration{TargetBackendID: "primary", TargetBucketName: trashTarget, CopyStartedAt: copyStart, ReboundAt: rebindAt}
			o := ObjectRef{Collection: "docs", Key: "a.bin", SizeBytes: cleanupSize, WrittenAt: tc.written, CaughtUp: tc.caughtUp}
			err := (&StorageMigrationWorker{Header: s, Identity: s}).verifyObject(context.Background(), m, o)
			if (err != nil) != tc.fails {
				t.Fatalf("verify = %v, want a failure: %v", err, tc.fails)
			}
			if err != nil && !strings.Contains(verifyReason(err), "content differs") {
				t.Errorf("reason = %q, want it to name the content", verifyReason(err))
			}
		})
	}
}

// A copy the catch-up made for a migration no longer as it was read is not
// recorded, and stops the pass.
func TestCatchUpStopsForAMigrationThatMoved(t *testing.T) {
	o := writtenBehind("a.bin", copyStart.Add(time.Minute))
	repo := rebindingRepo(o)
	repo.catchUpErr = ErrMigrationMoved
	s := newBucketStore()
	s.sizes[trashSource]["docs/a.bin"] = cleanupSize
	w := &StorageMigrationWorker{Repo: repo, Copier: s, CopyBatch: 10}
	if err := w.stepRebinding(context.Background(), repo.mig); !errors.Is(err, ErrMigrationMoved) {
		t.Fatalf("stepRebinding = %v, want ErrMigrationMoved", err)
	}
	if repo.rebound {
		t.Fatal("rebound for a migration that moved")
	}
}
