package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeMigRepo is an in-memory MigrationRepo for one tenant's migration.
type fakeMigRepo struct {
	mig         StorageMigration
	objects     []ObjectRef
	bucketState string // provision_state the worker sees for the target bucket
	rebound     bool
}

func (f *fakeMigRepo) ListActive(context.Context, int) ([]StorageMigration, error) {
	// 'completed' is still active (cleanup pending); only cleaned/failed are terminal.
	if f.mig.State == MigStateCleaned || f.mig.State == MigStateFailed {
		return nil, nil
	}
	return []StorageMigration{f.mig}, nil
}

func (f *fakeMigRepo) MarkCleaned(_ context.Context, _ uuid.UUID) error {
	f.mig.State = MigStateCleaned
	return nil
}

func (f *fakeMigRepo) BucketProvisionState(context.Context, string, string) (string, error) {
	return f.bucketState, nil
}

func (f *fakeMigRepo) CountObjects(context.Context, uuid.UUID) (int64, error) {
	return int64(len(f.objects)), nil
}

func (f *fakeMigRepo) SetCopying(_ context.Context, _ uuid.UUID, total int64) error {
	f.mig.ObjectsTotal = total
	f.mig.State = MigStateCopying
	return nil
}

func (f *fakeMigRepo) ListObjects(_ context.Context, _ uuid.UUID, afterObjectKey, afterKey string, limit int) ([]ObjectRef, error) {
	out := []ObjectRef{}
	for _, o := range f.objects {
		if o.ObjectKey > afterObjectKey || (o.ObjectKey == afterObjectKey && o.Key > afterKey) {
			out = append(out, o)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeMigRepo) AdvanceCopy(_ context.Context, _ uuid.UUID, copied int64, cursorObjectKey, cursorKey string) error {
	f.mig.ObjectsCopied = copied
	f.mig.CursorObjectKey = cursorObjectKey
	f.mig.CursorKey = cursorKey
	return nil
}

func (f *fakeMigRepo) SetState(_ context.Context, _ uuid.UUID, state string) error {
	f.mig.State = state
	return nil
}

func (f *fakeMigRepo) RebindTenant(context.Context, uuid.UUID, string, string) error {
	f.rebound = true
	return nil
}

func (f *fakeMigRepo) Complete(_ context.Context, _ uuid.UUID) error {
	f.mig.State = MigStateCompleted
	return nil
}

func (f *fakeMigRepo) Fail(_ context.Context, _ uuid.UUID, _ string) error {
	f.mig.State = MigStateFailed
	return nil
}

// fakeCopier records every copy.
type fakeCopier struct {
	copies []string // "objectKey/key : srcBucket->dstBucket"
	failOn string   // key to fail on (transient), "" = never
}

func (c *fakeCopier) CopyObject(_ context.Context, src, dst CopyLocation) error {
	if c.failOn != "" && dst.Key == c.failOn {
		return errors.New("copy boom")
	}
	c.copies = append(c.copies, src.ObjectKey+"/"+src.Key+" : "+src.Bucket+"->"+dst.Bucket)
	return nil
}

// fakeDeleter records every source delete.
type fakeDeleter struct{ deleted []string }

func (d *fakeDeleter) DeleteObject(_ context.Context, loc CopyLocation) error {
	d.deleted = append(d.deleted, loc.ObjectKey+"/"+loc.Key+" @ "+loc.Bucket)
	return nil
}

func runToTerminal(t *testing.T, w *StorageMigrationWorker, repo *fakeMigRepo) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if repo.mig.State == MigStateCompleted || repo.mig.State == MigStateFailed {
			return
		}
		w.tick(context.Background())
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
			{ObjectKey: "docs", Key: "a.txt"},
			{ObjectKey: "docs", Key: "b.txt"},
			{ObjectKey: "docs", Key: "c.txt"},
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
		t.Fatal("object_keys were never rebound")
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
	w.tick(context.Background())
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
		objects:     []ObjectRef{{ObjectKey: "docs", Key: "a.txt"}},
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
	w.tick(context.Background())
	if repo.mig.State != MigStateFailed {
		t.Fatalf("state = %q, want failed (must not rebind on incomplete copy)", repo.mig.State)
	}
	if repo.rebound {
		t.Fatal("SECURITY: rebound object_keys despite an incomplete copy — data would be orphaned")
	}
}

// fakeHeader answers HEADs against a target-bucket size map.
type fakeHeader struct{ sizes map[string]int64 }

func (h *fakeHeader) HeadObject(_ context.Context, loc CopyLocation) (int64, error) {
	if s, ok := h.sizes[loc.ObjectKey+"/"+loc.Key]; ok {
		return s, nil
	}
	return -1, errors.New("not found")
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
		objects:     []ObjectRef{{ObjectKey: "docs", Key: "a.txt", SizeBytes: 11}, {ObjectKey: "docs", Key: "b.txt", SizeBytes: 22}},
	}
	hdr := &fakeHeader{sizes: map[string]int64{"docs/a.txt": 11, "docs/b.txt": 22}} // sizes match
	w := &StorageMigrationWorker{Repo: repo, Copier: &fakeCopier{}, Header: hdr, CopyBatch: 2, Now: time.Now}
	runToTerminal(t, w, repo)
	if repo.mig.State != MigStateCompleted {
		t.Fatalf("state = %q, want completed (physical verify should pass)", repo.mig.State)
	}
}

func TestStorageMigration_PhysicalVerifyFailsOnMismatch(t *testing.T) {
	tid := uuid.New()
	repo := &fakeMigRepo{
		mig: StorageMigration{
			TenantID: tid, State: MigStateVerifying, ObjectsTotal: 1, ObjectsCopied: 1,
			TargetBackendID: "primary", TargetBucketName: "paladin-" + tid.String(),
		},
		objects: []ObjectRef{{ObjectKey: "docs", Key: "a.txt", SizeBytes: 100}},
	}
	hdr := &fakeHeader{sizes: map[string]int64{"docs/a.txt": 50}} // wrong size in target
	w := &StorageMigrationWorker{Repo: repo, Copier: &fakeCopier{}, Header: hdr, CopyBatch: 2, Now: time.Now}
	w.tick(context.Background())
	if repo.mig.State != MigStateFailed {
		t.Fatalf("state = %q, want failed (target size mismatch)", repo.mig.State)
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
		objects: []ObjectRef{{ObjectKey: "docs", Key: "a.txt"}, {ObjectKey: "docs", Key: "b.txt"}},
	}
	del := &fakeDeleter{}
	w := &StorageMigrationWorker{Repo: repo, Deleter: del, CopyBatch: 2, Now: time.Now}
	w.tick(context.Background())

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
		objects: []ObjectRef{{ObjectKey: "docs", Key: "a.txt"}},
	}
	del := &fakeDeleter{}
	w := &StorageMigrationWorker{Repo: repo, Deleter: del, CopyBatch: 2, Now: time.Now}
	w.tick(context.Background())

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
