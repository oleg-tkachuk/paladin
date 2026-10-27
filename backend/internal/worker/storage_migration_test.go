package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"
)

// fakeMigRepo is an in-memory MigrationRepo for one tenant's migration.
type fakeMigRepo struct {
	mig         StorageMigration
	objects     []ObjectRef
	bucketState string // provision_state the worker sees for the target bucket
	rebound     bool
	rebindErr   error // what RebindTenant returns; nil rebinds
	// written is what ListWrittenSince walks: every object with the time it
	// was last written, in (UpdatedAt, ID) order.
	written     []WrittenObject
	reboundBack bool   // RebindBack ran: the tenant is on its source again
	reason      string // the error StartRollback or Fail recorded
	reboundAt   time.Time
}

func (f *fakeMigRepo) ListActive(context.Context, int) ([]StorageMigration, error) {
	// 'completed' is still active (cleanup pending); only cleaned/failed are terminal.
	if f.mig.State == MigStateCleaned || f.mig.State == MigStateFailed {
		return nil, nil
	}
	return []StorageMigration{f.mig}, nil
}

// holds reports whether the migration is still as the caller read it: the
// same state and the same attempt, as the SQL guard compares.
func (f *fakeMigRepo) holds(at MigrationRead) bool {
	return f.mig.State == at.State && f.mig.Attempts == at.Attempts
}

// moveFrom applies a transition the way the SQL does: only from the
// migration as the caller read it.
func (f *fakeMigRepo) moveFrom(at MigrationRead, to string) error {
	if !f.holds(at) {
		return ErrMigrationMoved
	}
	f.mig.State = to
	return nil
}

func (f *fakeMigRepo) MarkCleaned(_ context.Context, at MigrationRead) error {
	return f.moveFrom(at, MigStateCleaned)
}

func (f *fakeMigRepo) BucketProvisionState(context.Context, string, string) (string, error) {
	return f.bucketState, nil
}

func (f *fakeMigRepo) CountObjects(context.Context, uuid.UUID) (int64, error) {
	return int64(len(f.objects)), nil
}

func (f *fakeMigRepo) SetCopying(_ context.Context, at MigrationRead, total int64) error {
	if err := f.moveFrom(at, MigStateCopying); err != nil {
		return err
	}
	f.mig.ObjectsTotal = total
	return nil
}

func (f *fakeMigRepo) ListObjects(_ context.Context, _ uuid.UUID, afterCollection, afterKey string, limit int) ([]ObjectRef, error) {
	out := []ObjectRef{}
	for _, o := range f.objects {
		if o.Collection > afterCollection || (o.Collection == afterCollection && o.Key > afterKey) {
			out = append(out, o)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeMigRepo) AdvanceCopy(_ context.Context, at MigrationRead, copied int64, cursorCollection, cursorKey string) error {
	if !f.holds(at) || copied < f.mig.ObjectsCopied {
		return ErrMigrationMoved
	}
	f.mig.ObjectsCopied = copied
	f.mig.CursorCollection = cursorCollection
	f.mig.CursorKey = cursorKey
	return nil
}

func (f *fakeMigRepo) SetState(_ context.Context, at MigrationRead, to string) error {
	return f.moveFrom(at, to)
}

func (f *fakeMigRepo) RebindTenant(_ context.Context, at MigrationRead, _, _ string) error {
	if !f.holds(at) {
		return ErrMigrationMoved
	}
	if f.rebindErr != nil {
		return f.rebindErr
	}
	f.rebound = true
	f.mig.ReboundAt = f.reboundAt
	return nil
}

func (f *fakeMigRepo) StartRollback(_ context.Context, at MigrationRead, reason string) error {
	if err := f.moveFrom(at, MigStateRollingBack); err != nil {
		return err
	}
	f.mig.Attempts++
	f.reason = reason
	f.mig.RollbackCursorAt, f.mig.RollbackCursorID = time.Time{}, uuid.Nil()
	return nil
}

// after is the SQL's ROW(updated_at, id) > ROW(at, id).
func after(o WrittenObject, at time.Time, id uuid.UUID) bool {
	return o.UpdatedAt.After(at) || (o.UpdatedAt.Equal(at) && bytes.Compare(o.ID[:], id[:]) > 0)
}

func (f *fakeMigRepo) ListWrittenSince(_ context.Context, _ uuid.UUID, afterAt time.Time, afterID uuid.UUID, limit int) ([]WrittenObject, error) {
	out := []WrittenObject{}
	for _, o := range f.written {
		if after(o, afterAt, afterID) {
			out = append(out, o)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeMigRepo) AdvanceRollback(_ context.Context, at MigrationRead, cursorAt time.Time, cursorID uuid.UUID) error {
	if !f.holds(at) {
		return ErrMigrationMoved
	}
	if !f.mig.RollbackCursorAt.IsZero() && after(WrittenObject{UpdatedAt: f.mig.RollbackCursorAt, ID: f.mig.RollbackCursorID}, cursorAt, cursorID) {
		return ErrMigrationMoved // backwards
	}
	f.mig.RollbackCursorAt, f.mig.RollbackCursorID = cursorAt, cursorID
	return nil
}

func (f *fakeMigRepo) RebindBack(_ context.Context, at MigrationRead, cursorAt time.Time, cursorID uuid.UUID, _, _ string) error {
	if !f.holds(at) {
		return ErrMigrationMoved
	}
	for _, o := range f.written {
		if after(o, cursorAt, cursorID) {
			return ErrRollbackBehind
		}
	}
	f.reboundBack = true
	f.mig.State = MigStateFailed // the error stays, attempts too
	return nil
}

func (f *fakeMigRepo) Complete(_ context.Context, at MigrationRead) error {
	return f.moveFrom(at, MigStateCompleted)
}

func (f *fakeMigRepo) Fail(_ context.Context, at MigrationRead, reason string) error {
	if err := f.moveFrom(at, MigStateFailed); err != nil {
		return err
	}
	f.mig.Attempts++
	f.reason = reason
	return nil
}

// fakeCopier records every copy.
type fakeCopier struct {
	copies []string // "collection/key : srcBucket->dstBucket"
	failOn string   // key to fail on (transient), "" = never
}

func (c *fakeCopier) CopyObject(_ context.Context, src, dst CopyLocation) error {
	if c.failOn != "" && dst.Key == c.failOn {
		return errors.New("copy boom")
	}
	c.copies = append(c.copies, src.Collection+"/"+src.Key+" : "+src.Bucket+"->"+dst.Bucket)
	return nil
}

// fakeDeleter records every source delete.
type fakeDeleter struct{ deleted []string }

func (d *fakeDeleter) DeleteObject(_ context.Context, loc CopyLocation) error {
	d.deleted = append(d.deleted, loc.Collection+"/"+loc.Key+" @ "+loc.Bucket)
	return nil
}

func runToTerminal(t *testing.T, w *StorageMigrationWorker, repo *fakeMigRepo) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if repo.mig.State == MigStateCompleted || repo.mig.State == MigStateFailed {
			return
		}
		if err := w.tick(context.Background()); err != nil {
			t.Fatalf("tick in %q = %v, want nil on the way to a terminal state", repo.mig.State, err)
		}
	}
	t.Fatalf("migration did not reach a terminal state (stuck in %q)", repo.mig.State)
}

func newWorker(repo *fakeMigRepo, cop *fakeCopier) *StorageMigrationWorker {
	return &StorageMigrationWorker{Repo: repo, Copier: cop, CopyBatch: 2}
}

func TestStorageMigration_HappyPath(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateProvisioning,
			SourceBackendID: "primary", SourceBucketName: "paladin-shared",
			TargetBackendID: "primary", TargetBucketName: "paladin-" + tid.String(),
		},
		bucketState: "ready",
		objects: []ObjectRef{
			{Collection: "docs", Key: "a.txt"},
			{Collection: "docs", Key: "b.txt"},
			{Collection: "docs", Key: "c.txt"},
		},
	}
	cop := &fakeCopier{}
	runToTerminal(t, newWorker(repo, cop), repo)

	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q, want completed", repo.mig.State)
	}
	if len(cop.copies) != 3 {
		t.Fatalf("copied %d objects, want 3: %v", len(cop.copies), cop.copies)
	}
	if repo.mig.ObjectsCopied != 3 {
		t.Fatalf("objects_copied = %d, want 3", repo.mig.ObjectsCopied)
	}
	if !repo.rebound {
		t.Fatal("collections were never rebound")
	}
	// Every copy is shared -> dedicated on the same backend, identical key.
	for _, c := range cop.copies {
		if !contains(c, "paladin-shared->paladin-"+tid.String()) {
			t.Fatalf("copy did not go shared->dedicated: %q", c)
		}
	}
}

func TestStorageMigration_WaitsForBucketReady(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig:         StorageMigration{TenantID: tid, State: MigStateProvisioning, SourceBackendID: "primary", TargetBackendID: "primary"},
		bucketState: "pending", // reconciler hasn't created it yet
	}
	w := newWorker(repo, &fakeCopier{})
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateProvisioning {
		t.Fatalf("state = %q, want provisioning (should wait for the bucket)", repo.mig.State)
	}
}

func TestStorageMigration_CrossBackendProceeds(t *testing.T) {
	// Slice 3: cross-backend is supported (the s3 router stream-throughs), so the
	// worker must migrate it end to end, not fail it.
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateProvisioning,
			SourceBackendID: "primary", SourceBucketName: "paladin-shared",
			TargetBackendID: "secondary", TargetBucketName: "paladin-" + tid.String(),
		},
		bucketState: "ready",
		objects:     []ObjectRef{{Collection: "docs", Key: "a.txt"}},
	}
	cop := &fakeCopier{}
	runToTerminal(t, newWorker(repo, cop), repo)
	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q, want completed (cross-backend now supported)", repo.mig.State)
	}
	if len(cop.copies) != 1 {
		t.Fatalf("copied %d, want 1 (cross-backend copy)", len(cop.copies))
	}
}

// TestStorageMigration_IncompleteCopyDoesNotRebind guards against the RLS-hidden
// -rows bug: if the object list ends while copied < total, the worker must fail
// rather than rebind (which would orphan the uncopied objects).
func TestStorageMigration_IncompleteCopyDoesNotRebind(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateCopying,
			ObjectsTotal: 5, ObjectsCopied: 2, // 3 objects unaccounted for
			SourceBackendID: "primary", TargetBackendID: "primary",
		},
		objects: nil, // list returns empty even though total=5
	}
	w := newWorker(repo, &fakeCopier{})
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateFailed {
		t.Fatalf("state = %q, want failed (must not rebind on incomplete copy)", repo.mig.State)
	}
	if repo.rebound {
		t.Fatal("SECURITY: rebound collections despite an incomplete copy — data would be orphaned")
	}
}

// A rebind the schema refuses (a public collection it will not let move,
// ADR-0027) fails the migration instead of being retried every tick forever.
func TestStorageMigration_RefusedRebindFails(t *testing.T) {
	repo := &fakeMigRepo{
		mig: StorageMigration{TenantID: uuid.New(), State: MigStateRebinding},
		rebindErr: fmt.Errorf("rebind %q: %w: %w", "photos", ErrRebindRefused,
			errors.New("public collection photos may not move to another bucket")),
	}
	w := newWorker(repo, &fakeCopier{})
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateFailed {
		t.Fatalf("state = %q, want failed", repo.mig.State)
	}
	if repo.rebound {
		t.Fatal("rebound despite the refusal")
	}
}

// Any other rebind error is transient: the migration stays put for the next
// tick.
func TestStorageMigration_TransientRebindErrorRetries(t *testing.T) {
	repo := &fakeMigRepo{
		mig:       StorageMigration{TenantID: uuid.New(), State: MigStateRebinding},
		rebindErr: errors.New("connection reset"),
	}
	w := newWorker(repo, &fakeCopier{})
	// A transient step failure is a failed tick, which the alert reads.
	if err := w.tick(context.Background()); !errors.Is(err, repo.rebindErr) {
		t.Errorf("tick = %v, want the rebind failure %v", err, repo.rebindErr)
	}
	if repo.mig.State != MigStateRebinding {
		t.Fatalf("state = %q, want %q (retried next tick)", repo.mig.State, MigStateRebinding)
	}
}

// fakeHeader answers HEADs against a target-bucket size map.
type fakeHeader struct{ sizes map[string]int64 }

func (h *fakeHeader) HeadObject(_ context.Context, loc CopyLocation) (int64, error) {
	if s, ok := h.sizes[loc.Collection+"/"+loc.Key]; ok {
		return s, nil
	}
	return -1, fmt.Errorf("head: %w", ErrObjectMissing)
}

func TestStorageMigration_PhysicalVerifyPass(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateProvisioning,
			SourceBackendID: "primary", SourceBucketName: "paladin-shared",
			TargetBackendID: "primary", TargetBucketName: "paladin-" + tid.String(),
		},
		bucketState: "ready",
		objects:     []ObjectRef{{Collection: "docs", Key: "a.txt", SizeBytes: 11}, {Collection: "docs", Key: "b.txt", SizeBytes: 22}},
	}
	hdr := &fakeHeader{sizes: map[string]int64{"docs/a.txt": 11, "docs/b.txt": 22}} // sizes match
	w := &StorageMigrationWorker{Repo: repo, Copier: &fakeCopier{}, Header: hdr, CopyBatch: 2, Now: time.Now}
	runToTerminal(t, w, repo)
	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q, want completed (physical verify should pass)", repo.mig.State)
	}
}

// rollbackFixture is a migration that copied docs/a.txt broken (50 bytes of
// 100) and rebound the tenant at reboundAt. After the rebind the tenant wrote:
// new.txt (in the target, as recorded), a.txt's tags (its row moved, its
// broken target copy did not), late.txt (an upload that resolved its bucket
// before the rebind, so not in the target) — and five more, to need batches.
func rollbackFixture(state string) (*fakeMigRepo, *fakeHeader) {
	tid := uuid.New()
	reboundAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	written := func(key string, sec int, size int64) WrittenObject {
		return WrittenObject{
			ObjectRef: ObjectRef{Collection: "docs", Key: key, SizeBytes: size},
			ID:        uuid.New(), UpdatedAt: reboundAt.Add(time.Duration(sec) * time.Second),
		}
	}
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: state, ObjectsTotal: 2, ObjectsCopied: 2,
			SourceBackendID: "primary", SourceBucketName: "paladin-shared",
			TargetBackendID: "primary", TargetBucketName: "paladin-" + tid.String(),
			ReboundAt: reboundAt, Attempts: 3,
		},
		objects: []ObjectRef{{Collection: "docs", Key: "a.txt", SizeBytes: 100}, {Collection: "docs", Key: "b.txt", SizeBytes: 7}},
		rebound: true,
		written: []WrittenObject{
			written("new.txt", 0, 9), // at the rebind instant: included
			written("a.txt", 1, 100),
			written("late.txt", 2, 4),
			written("w1", 3, 1), written("w2", 4, 1), written("w3", 5, 1), written("w4", 6, 1), written("w5", 7, 1),
		},
	}
	hdr := &fakeHeader{sizes: map[string]int64{
		"docs/a.txt": 50, "docs/b.txt": 7, "docs/new.txt": 9,
		"docs/w1": 1, "docs/w2": 1, "docs/w3": 1, "docs/w4": 1, "docs/w5": 1,
	}}
	return repo, hdr
}

// A verify that fails after the rebind used to fail the migration in place:
// the tenant stayed dedicated on a bucket missing objects, with no way back.
// It now rolls back: what the tenant wrote to the target since the rebind is
// copied back to the source, the tenant is rebound to it, and the migration
// ends 'failed' with the verify error — retryable.
func TestStorageMigration_VerifyFailureRollsBack(t *testing.T) {
	repo, hdr := rollbackFixture(MigStateVerifying)
	cop := &fakeCopier{}
	del := &fakeDeleter{}
	w := &StorageMigrationWorker{Repo: repo, Copier: cop, Deleter: del, Header: hdr, CopyBatch: 2,
		Now: func() time.Time { return time.Now().Add(24 * time.Hour) }}

	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateRollingBack {
		t.Fatalf("state = %q after the failed verify, want %q", repo.mig.State, MigStateRollingBack)
	}
	if repo.mig.Attempts != 4 {
		t.Errorf("attempts = %d, want the verify failure counted (4)", repo.mig.Attempts)
	}
	runToTerminal(t, w, repo)

	if repo.mig.State != MigStateFailed || !repo.reboundBack {
		t.Fatalf("state = %q, rebound back = %v; want failed on the source bucket", repo.mig.State, repo.reboundBack)
	}
	if !contains(repo.reason, "physical verify") || !contains(repo.reason, "a.txt size mismatch") {
		t.Errorf("error = %q, want the verify failure kept", repo.reason)
	}
	if repo.mig.Attempts != 4 {
		t.Errorf("attempts = %d after the rollback, want 4 (the failure counted once)", repo.mig.Attempts)
	}
	want := map[string]bool{}
	for _, k := range []string{"new.txt", "w1", "w2", "w3", "w4", "w5"} {
		want["docs/"+k+" : "+repo.mig.TargetBucketName+"->paladin-shared"] = true
	}
	for _, c := range cop.copies {
		if !want[c] {
			t.Errorf("unexpected copy %q (a.txt's broken copy and late.txt, absent from the target, must not be copied)", c)
		}
		delete(want, c)
	}
	for c := range want {
		t.Errorf("missing copy %q", c)
	}
	if len(del.deleted) != 0 {
		t.Errorf("a rolled-back migration deleted source objects: %v", del.deleted)
	}
}

// A reverse copy that keeps failing leaves the migration rolling back — the
// tenant on the target, whole — and the next ticks retry it from the cursor.
func TestStorageMigration_RollbackCopyFailureRetries(t *testing.T) {
	repo, hdr := rollbackFixture(MigStateRollingBack)
	cop := &fakeCopier{failOn: "w2"}
	w := &StorageMigrationWorker{Repo: repo, Copier: cop, Header: hdr, CopyBatch: 2, Now: time.Now}
	var tickErr error
	for range 5 {
		tickErr = w.tick(context.Background())
	}
	// The first batch copies what precedes w2; every tick after it is stuck.
	if tickErr == nil {
		t.Error("tick = nil while the reverse copy keeps failing, want the failure")
	}
	if repo.mig.State != MigStateRollingBack || repo.reboundBack {
		t.Fatalf("state = %q, rebound back = %v; want still rolling back on the target", repo.mig.State, repo.reboundBack)
	}
	if got := repo.mig.RollbackCursorAt; !got.Equal(repo.written[3].UpdatedAt) {
		t.Errorf("cursor at %v, want it kept at w1 (%v), the last object copied", got, repo.written[3].UpdatedAt)
	}

	cop.failOn = ""
	runToTerminal(t, w, repo)
	if repo.mig.State != MigStateFailed || !repo.reboundBack {
		t.Fatalf("state = %q, rebound back = %v; want failed on the source bucket", repo.mig.State, repo.reboundBack)
	}
	if !contains(fmt.Sprint(cop.copies), "docs/w2") {
		t.Errorf("w2 never copied back: %v", cop.copies)
	}
}

// An object written past the cursor after the last batch is still only in
// the target: the rebind back refuses, and the next tick copies it first.
func TestStorageMigration_RollbackCatchesUpBeforeRebindingBack(t *testing.T) {
	repo, hdr := rollbackFixture(MigStateRollingBack)
	repo.written = repo.written[:1]
	cop := &fakeCopier{}
	w := &StorageMigrationWorker{Repo: repo, Copier: cop, Header: hdr, CopyBatch: 2, Now: time.Now}
	if err := w.tick(context.Background()); err != nil { // copies new.txt
		t.Fatalf("tick = %v, want nil", err)
	}
	// Written now, after the batch: the RebindBack below must not go ahead.
	repo.written = append(repo.written, WrittenObject{
		ObjectRef: ObjectRef{Collection: "docs", Key: "w1", SizeBytes: 1},
		ID:        uuid.New(), UpdatedAt: repo.mig.ReboundAt.Add(time.Minute),
	})
	if err := w.Repo.RebindBack(context.Background(), repo.mig.Read(), repo.mig.RollbackCursorAt,
		repo.mig.RollbackCursorID, "primary", "paladin-shared"); !errors.Is(err, ErrRollbackBehind) {
		t.Fatalf("RebindBack = %v, want ErrRollbackBehind", err)
	}
	runToTerminal(t, w, repo)
	if !contains(fmt.Sprint(cop.copies), "docs/w1") || !repo.reboundBack {
		t.Errorf("copies %v, rebound back %v; want w1 copied before rebinding back", cop.copies, repo.reboundBack)
	}
}

// A rollback step read by a worker that then paused changes nothing once the
// migration moved on — another attempt, or another state: no copy over what
// the source holds now, no cursor, no rebind.
func TestStorageMigration_StaleRollbackChangesNothing(t *testing.T) {
	for name, move := range map[string]func(m *StorageMigration){
		"another attempt": func(m *StorageMigration) { m.Attempts++ },
		// Rolled back, retried and copying again: the tenant is shared and
		// writing to the source.
		"another state": func(m *StorageMigration) { m.State = MigStateCopying; m.Attempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			repo, hdr := rollbackFixture(MigStateRollingBack)
			stale := repo.mig
			move(&repo.mig)
			current := repo.mig
			cop := &fakeCopier{}
			w := &StorageMigrationWorker{Repo: repo, Copier: cop, Header: hdr, CopyBatch: 2, Now: time.Now}

			if err := w.advance(context.Background(), stale); !errors.Is(err, ErrMigrationMoved) {
				t.Fatalf("stale step returned %v, want ErrMigrationMoved", err)
			}
			if repo.mig != current || repo.reboundBack || len(cop.copies) != 0 {
				t.Errorf("stale step changed things: %+v (want %+v), rebound back %v, copies %v",
					repo.mig, current, repo.reboundBack, cop.copies)
			}
			// Nor, with the batch done, its rebind back.
			stale.RollbackCursorAt = repo.written[len(repo.written)-1].UpdatedAt
			stale.RollbackCursorID = repo.written[len(repo.written)-1].ID
			if err := w.advance(context.Background(), stale); !errors.Is(err, ErrMigrationMoved) {
				t.Fatalf("stale rebind back returned %v, want ErrMigrationMoved", err)
			}
			if repo.reboundBack || repo.mig != current {
				t.Error("a stale rebind back went through")
			}
		})
	}
}

func TestStorageMigration_CleanupAfterRetention(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateCompleted,
			SourceBackendID: "primary", SourceBucketName: "paladin-shared",
			TargetBackendID: "primary", TargetBucketName: "paladin-" + tid.String(),
			ObjectsTotal: 2, ObjectsCopied: 2,
			CleanupAfter: time.Now().Add(-time.Minute), // retention elapsed
		},
		objects: []ObjectRef{{Collection: "docs", Key: "a.txt"}, {Collection: "docs", Key: "b.txt"}},
	}
	del := &fakeDeleter{}
	w := &StorageMigrationWorker{Repo: repo, Deleter: del, CopyBatch: 2, Now: time.Now}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}

	if repo.mig.State != MigStateCleaned {
		t.Fatalf("state = %q, want cleaned", repo.mig.State)
	}
	if len(del.deleted) != 2 {
		t.Fatalf("deleted %d source objects, want 2: %v", len(del.deleted), del.deleted)
	}
	for _, d := range del.deleted {
		if !contains(d, "@ paladin-shared") {
			t.Fatalf("delete did not target the SOURCE bucket: %q", d)
		}
	}
}

func TestStorageMigration_CleanupWaitsForRetention(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig:     StorageMigration{TenantID: tid, State: MigStateCompleted, CleanupAfter: time.Now().Add(time.Hour)},
		objects: []ObjectRef{{Collection: "docs", Key: "a.txt"}},
	}
	del := &fakeDeleter{}
	w := &StorageMigrationWorker{Repo: repo, Deleter: del, CopyBatch: 2, Now: time.Now}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}

	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q, want completed (retention not elapsed)", repo.mig.State)
	}
	if len(del.deleted) != 0 {
		t.Fatal("deleted source objects before the retention window elapsed")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A worker paused past its lease resumes with the migration as it read it.
// Its successor has since finished the copy, rebound the tenant and moved to
// verifying. The stale copy step used to write its smaller count and older
// cursor over that, leaving verify failing on "copied < total" for good.
func TestStorageMigration_StaleStepDoesNotDragItBack(t *testing.T) {
	objects := []ObjectRef{{Collection: "c", Key: "a"}, {Collection: "c", Key: "b"}, {Collection: "c", Key: "d"}}
	stale := StorageMigration{
		TenantID: uuid.New(), State: MigStateCopying,
		SourceBucketName: "shared", TargetBucketName: "dedicated",
		ObjectsTotal: int64(len(objects)), ObjectsCopied: 1, CursorCollection: "c", CursorKey: "a",
	}
	current := stale
	current.State = MigStateVerifying
	current.ObjectsCopied = int64(len(objects))
	current.CursorKey = "d"
	repo := &fakeMigRepo{mig: current, objects: objects, rebound: true}
	w := newWorker(repo, &fakeCopier{})

	if err := w.advance(context.Background(), stale); !errors.Is(err, ErrMigrationMoved) {
		t.Fatalf("stale step returned %v, want ErrMigrationMoved", err)
	}
	if repo.mig.State != MigStateVerifying || repo.mig.ObjectsCopied != current.ObjectsCopied || repo.mig.CursorKey != "d" {
		t.Errorf("stale step moved the migration to %s, %d copied, cursor %q; want it left at %s, %d, %q",
			repo.mig.State, repo.mig.ObjectsCopied, repo.mig.CursorKey,
			MigStateVerifying, current.ObjectsCopied, "d")
	}

	// Its successor's next tick still completes it.
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if repo.mig.State != MigStateCompleted {
		t.Errorf("state = %s after the successor's tick, want %s", repo.mig.State, MigStateCompleted)
	}
}

// The state alone repeats across attempts. A worker read a migration copying
// on its first attempt and paused; meanwhile the attempt failed, an operator
// retried it, and the retry is copying again from the start. The stale step
// matches on state, so only the attempt keeps its count and cursor out of the
// retry — and a stale rebind from pointing the tenant at a bucket the retry
// has not filled.
func TestStorageMigration_StaleStepFromAnEarlierAttemptChangesNothing(t *testing.T) {
	objects := []ObjectRef{{Collection: "c", Key: "a"}, {Collection: "c", Key: "b"}, {Collection: "c", Key: "d"}}
	read := StorageMigration{
		TenantID: uuid.New(), State: MigStateCopying,
		SourceBucketName: "shared", TargetBucketName: "dedicated",
		ObjectsTotal: int64(len(objects)), ObjectsCopied: 1, CursorCollection: "c", CursorKey: "a",
	}

	for _, state := range []string{MigStateCopying, MigStateRebinding} {
		t.Run(state, func(t *testing.T) {
			stale := read
			stale.State = state
			// The retry: same state, progress reset, one failure further on.
			retry := read
			retry.State = state
			retry.ObjectsCopied, retry.CursorCollection, retry.CursorKey = 0, "", ""
			retry.Attempts = stale.Attempts + 1
			repo := &fakeMigRepo{mig: retry, objects: objects}
			w := newWorker(repo, &fakeCopier{})

			if err := w.advance(context.Background(), stale); !errors.Is(err, ErrMigrationMoved) {
				t.Fatalf("stale step returned %v, want ErrMigrationMoved", err)
			}
			if repo.mig != retry {
				t.Errorf("stale step changed the retry: %+v, want %+v", repo.mig, retry)
			}
			if repo.rebound {
				t.Error("a stale rebind repointed the tenant at the retry's unfilled bucket")
			}
		})
	}
}
