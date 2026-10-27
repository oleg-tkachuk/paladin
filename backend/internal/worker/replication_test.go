package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

type fakeReplSource struct {
	buckets  []admindomain.Bucket
	bindings map[string][]CollectionBinding
	listErr  error
}

func (f *fakeReplSource) ListBucketsWithReplication(_ context.Context) ([]admindomain.Bucket, error) {
	return f.buckets, f.listErr
}
func (f *fakeReplSource) ListCollectionBindings(_ context.Context, backend, bucket string) ([]CollectionBinding, error) {
	return f.bindings[backend+"/"+bucket], nil
}

type fakeReplicator struct {
	mu     sync.Mutex
	copies []ReplicationTarget // dst targets, for inspection
	fail   bool
}

func (f *fakeReplicator) Replicate(_ context.Context, _ ReplicationTarget, dst ReplicationTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errSpecificCopyFailure
	}
	f.copies = append(f.copies, dst)
	return nil
}

var errSpecificCopyFailure = &replicationParseError{name: "synthetic"}

func TestSplitBucketName(t *testing.T) {
	cases := map[string]struct {
		backend, bucket string
		errish          bool
	}{
		"storageBackends/primary/buckets/archive": {backend: "primary", bucket: "archive"},
		"":          {errish: true},
		"buckets/x": {errish: true},
	}
	for in, want := range cases {
		gotBackend, gotBucket, err := splitBucketName(in)
		if want.errish {
			if err == nil {
				t.Errorf("%q: expected error", in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", in, err)
		}
		if gotBackend != want.backend || gotBucket != want.bucket {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", in, gotBackend, gotBucket, want.backend, want.bucket)
		}
	}
}

func TestReplicationWorkerCopiesAvailableNewerThanCutoff(t *testing.T) {
	tenantID := uuid.NewV7()
	now := time.Now()
	src := &fakeReplSource{
		buckets: []admindomain.Bucket{{
			BackendID:  "primary",
			BucketName: "paladin-archive",
			Replication: admindomain.BucketReplication{
				Enabled:           true,
				DestinationBucket: "storageBackends/secondary/buckets/paladin-archive-dr",
			},
		}},
		bindings: map[string][]CollectionBinding{
			"primary/paladin-archive": {{TenantID: tenantID, Collection: "k"}},
		},
	}
	committedNew := now.Add(-30 * time.Second)
	committedOld := now.Add(-2 * time.Hour) // outside lookback
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.NewV7(), State: "AVAILABLE", CommittedAt: &committedNew},
		{ObjectID: uuid.NewV7(), State: "AVAILABLE", CommittedAt: &committedOld},
		{ObjectID: uuid.NewV7(), State: "PENDING", CommittedAt: &committedNew}, // skipped
	}}
	rep := &fakeReplicator{}
	w := &ReplicationWorker{
		Buckets:        src,
		Objects:        objs,
		Replicator:     rep,
		LookbackWindow: 1 * time.Hour,
		Now:            func() time.Time { return now },
		watermarks:     map[string]time.Time{},
	}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}

	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.copies) != 1 {
		t.Fatalf("expected one copy, got %d", len(rep.copies))
	}
	if rep.copies[0].BackendID != "secondary" || rep.copies[0].BucketName != "paladin-archive-dr" {
		t.Errorf("dst: got %+v", rep.copies[0])
	}
}

func TestReplicationWorkerSkipsDisabledBuckets(t *testing.T) {
	src := &fakeReplSource{
		buckets: []admindomain.Bucket{{
			BackendID:  "primary",
			BucketName: "paladin-archive",
			Replication: admindomain.BucketReplication{
				Enabled: false, // disabled
			},
		}},
	}
	rep := &fakeReplicator{}
	w := &ReplicationWorker{
		Buckets: src, Replicator: rep, watermarks: map[string]time.Time{},
		Now: time.Now,
	}
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	if len(rep.copies) != 0 {
		t.Errorf("expected no copies for disabled bucket, got %d", len(rep.copies))
	}
}

func TestReplicationWorkerDryRunWithNilReplicator(t *testing.T) {
	tenantID := uuid.NewV7()
	now := time.Now()
	src := &fakeReplSource{
		buckets: []admindomain.Bucket{{
			BackendID:  "primary",
			BucketName: "paladin-archive",
			Replication: admindomain.BucketReplication{
				Enabled:           true,
				DestinationBucket: "storageBackends/secondary/buckets/paladin-archive-dr",
			},
		}},
		bindings: map[string][]CollectionBinding{
			"primary/paladin-archive": {{TenantID: tenantID, Collection: "k"}},
		},
	}
	committed := now.Add(-30 * time.Second)
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.NewV7(), State: "AVAILABLE", CommittedAt: &committed},
	}}
	w := &ReplicationWorker{
		Buckets: src, Objects: objs, Replicator: nil,
		LookbackWindow: 1 * time.Hour,
		Now:            func() time.Time { return now },
		watermarks:     map[string]time.Time{},
	}
	if err := w.tick(context.Background()); err != nil { // should not panic
		t.Fatalf("tick = %v, want nil", err)
	}
}

// The skip guard is a disjunction: replication off OR no destination. The
// disabled-bucket test above sets BOTH halves true, so it cannot tell the
// operators apart — joining them with && still skips that bucket and the
// mutation survives.
//
// Isolating a half is not enough on its own either: the fixture has to be one
// that WOULD copy if the guard let it through. The first attempt at this test
// used a bucket with no bindings and no objects, so nothing was copied whether
// the guard fired or not, and it passed against the mutant as happily as
// against the real code.
//
// So this starts from the copying fixture and changes exactly one field.
func TestReplicationWorkerSkipsADisabledBucketThatWouldOtherwiseCopy(t *testing.T) {
	tenantID := uuid.NewV7()
	now := time.Now()
	committed := now.Add(-30 * time.Second)

	src := &fakeReplSource{
		buckets: []admindomain.Bucket{{
			BackendID:  "primary",
			BucketName: "paladin-archive",
			Replication: admindomain.BucketReplication{
				// The only difference from the copying test: replication is
				// off, while a valid destination is still configured. Under
				// && this bucket replicates, copying a tenant's objects into
				// another bucket against the configuration that forbids it.
				Enabled:           false,
				DestinationBucket: "storageBackends/secondary/buckets/paladin-archive-dr",
			},
		}},
		bindings: map[string][]CollectionBinding{
			"primary/paladin-archive": {{TenantID: tenantID, Collection: "k"}},
		},
	}
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.NewV7(), State: "AVAILABLE", CommittedAt: &committed},
	}}
	rep := &fakeReplicator{}
	w := &ReplicationWorker{
		Buckets:        src,
		Objects:        objs,
		Replicator:     rep,
		LookbackWindow: time.Hour,
		Now:            func() time.Time { return now },
		watermarks:     map[string]time.Time{},
	}

	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}

	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.copies) != 0 {
		t.Errorf("%d objects were replicated out of a bucket whose replication "+
			"is disabled", len(rep.copies))
	}
}

// The lookback window, and the age of an object inside it.
const (
	replicationTestLookback = time.Hour
	replicationTestAge      = time.Minute
)

// replicatingFixture is one enabled bucket with one binding holding two
// objects inside the lookback window, so a tick copies both.
func replicatingFixture(now time.Time) (*fakeReplSource, *fakeObjIter, CollectionBinding, admindomain.Bucket) {
	bind := CollectionBinding{TenantID: uuid.NewV7(), Collection: "k"}
	bucket := admindomain.Bucket{
		BackendID:  "primary",
		BucketName: "paladin-archive",
		Replication: admindomain.BucketReplication{
			Enabled:           true,
			DestinationBucket: "storageBackends/secondary/buckets/paladin-archive-dr",
		},
	}
	src := &fakeReplSource{
		buckets:  []admindomain.Bucket{bucket},
		bindings: map[string][]CollectionBinding{bucket.BackendID + "/" + bucket.BucketName: {bind}},
	}
	committed := now.Add(-replicationTestAge)
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.NewV7(), Key: "a", State: "AVAILABLE", CommittedAt: &committed},
		{ObjectID: uuid.NewV7(), Key: "b", State: "AVAILABLE", CommittedAt: &committed},
	}}
	return src, objs, bind, bucket
}

// countingReplicator fails every copy and counts the attempts.
type countingReplicator struct {
	attempts int
	err      error
}

func (c *countingReplicator) Replicate(context.Context, ReplicationTarget, ReplicationTarget) error {
	c.attempts++
	return c.err
}

// A copy that fails is a failed tick, and the other objects are still copied:
// one unreachable object must not hold the bucket's replication back.
func TestReplicationWorkerTickReturnsCopyFailure(t *testing.T) {
	now := time.Now()
	src, objs, _, _ := replicatingFixture(now)
	rep := &countingReplicator{err: errSpecificCopyFailure}
	w := &ReplicationWorker{
		Buckets: src, Objects: objs, Replicator: rep,
		LookbackWindow: replicationTestLookback, Now: func() time.Time { return now },
		watermarks: map[string]time.Time{},
	}
	if err := w.tick(context.Background()); !errors.Is(err, errSpecificCopyFailure) {
		t.Errorf("tick = %v, want %v", err, errSpecificCopyFailure)
	}
	if rep.attempts != len(objs.rows) {
		t.Errorf("copy attempts = %d, want %d: a failure must not stop the rest", rep.attempts, len(objs.rows))
	}
}

func TestReplicationWorkerTickReturnsListFailure(t *testing.T) {
	src := &fakeReplSource{listErr: errors.New("bucket store unavailable")}
	w := &ReplicationWorker{Buckets: src, Now: time.Now, watermarks: map[string]time.Time{}}
	if err := w.tick(context.Background()); !errors.Is(err, src.listErr) {
		t.Errorf("tick = %v, want %v", err, src.listErr)
	}
}

// A watermark the store refuses is reported, although the copy landed.
func TestReplicationWorkerTickReturnsWatermarkFailure(t *testing.T) {
	now := time.Now()
	src, objs, _, _ := replicatingFixture(now)
	store := newFakeWatermarkStore()
	storeDown := errors.New("watermark store unavailable")
	store.advanceFn = func(time.Time) error { return storeDown }
	w := &ReplicationWorker{
		Buckets: src, Objects: objs, Replicator: &fakeReplicator{}, Watermarks: store,
		LookbackWindow: replicationTestLookback, Now: func() time.Time { return now },
		watermarks: map[string]time.Time{},
	}
	if err := w.tick(context.Background()); !errors.Is(err, storeDown) {
		t.Errorf("tick = %v, want %v", err, storeDown)
	}
}

// A per-object line names the object's tenant, collection and bucket, and
// where it was being copied to: an object_id alone does not say which
// replication pair is failing.
func TestReplicationWorkerCopyFailureNamesTheObject(t *testing.T) {
	now := time.Now()
	src, objs, bind, bucket := replicatingFixture(now)
	core, logs := observer.New(zapcore.WarnLevel)
	w := &ReplicationWorker{
		Buckets: src, Objects: objs, Replicator: &fakeReplicator{fail: true},
		LookbackWindow: replicationTestLookback, Now: func() time.Time { return now },
		Logger: zap.New(core), watermarks: map[string]time.Time{},
	}
	_ = w.tick(context.Background()) // fails by design; the log is what this test reads

	lines := logs.FilterMessage("failed to copy object").All()
	if len(lines) != len(objs.rows) {
		t.Fatalf("copy failure lines = %d, want %d", len(lines), len(objs.rows))
	}
	assertLogFields(t, lines[0], map[string]string{
		logKeyTenant:     bind.TenantID.String(),
		logKeyCollection: bind.Collection,
		logKeyBackend:    bucket.BackendID,
		logKeyBucket:     bucket.BucketName,
		logKeyDstBackend: "secondary",
		logKeyDstBucket:  "paladin-archive-dr",
		logKeyObjectID:   objs.rows[0].ObjectID.String(),
		logKeyKey:        objs.rows[0].Key,
	})
}
