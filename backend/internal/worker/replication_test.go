package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
)

type fakeReplSource struct {
	buckets  []admindomain.Bucket
	bindings map[string][]ObjectKeyBinding
}

func (f *fakeReplSource) ListBucketsWithReplication(_ context.Context) ([]admindomain.Bucket, error) {
	return f.buckets, nil
}
func (f *fakeReplSource) ListObjectKeyBindings(_ context.Context, backend, bucket string) ([]ObjectKeyBinding, error) {
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
	tenantID := uuid.Must(uuid.NewV7())
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
		bindings: map[string][]ObjectKeyBinding{
			"primary/paladin-archive": {{TenantID: tenantID, ObjectKey: "k"}},
		},
	}
	committedNew := now.Add(-30 * time.Second)
	committedOld := now.Add(-2 * time.Hour) // outside lookback
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.Must(uuid.NewV7()), State: "AVAILABLE", CommittedAt: &committedNew},
		{ObjectID: uuid.Must(uuid.NewV7()), State: "AVAILABLE", CommittedAt: &committedOld},
		{ObjectID: uuid.Must(uuid.NewV7()), State: "PENDING", CommittedAt: &committedNew}, // skipped
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
	w.tick(context.Background())

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
	w.tick(context.Background())
	if len(rep.copies) != 0 {
		t.Errorf("expected no copies for disabled bucket, got %d", len(rep.copies))
	}
}

func TestReplicationWorkerDryRunWithNilReplicator(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
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
		bindings: map[string][]ObjectKeyBinding{
			"primary/paladin-archive": {{TenantID: tenantID, ObjectKey: "k"}},
		},
	}
	committed := now.Add(-30 * time.Second)
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.Must(uuid.NewV7()), State: "AVAILABLE", CommittedAt: &committed},
	}}
	w := &ReplicationWorker{
		Buckets: src, Objects: objs, Replicator: nil,
		LookbackWindow: 1 * time.Hour,
		Now:            func() time.Time { return now },
		watermarks:     map[string]time.Time{},
	}
	w.tick(context.Background()) // should not panic
}
