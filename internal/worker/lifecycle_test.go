package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

type fakeBucketSource struct {
	buckets  []admindomain.Bucket
	bindings map[string][]ObjectKeyBinding
}

func (f *fakeBucketSource) ListBucketsWithLifecycle(_ context.Context) ([]admindomain.Bucket, error) {
	return f.buckets, nil
}
func (f *fakeBucketSource) ListObjectKeyBindings(_ context.Context, backend, bucket string) ([]ObjectKeyBinding, error) {
	return f.bindings[backend+"/"+bucket], nil
}

type fakeObjIter struct {
	rows []LifecycleObjectRow
}

func (f *fakeObjIter) IterateObjects(_ context.Context, _ uuid.UUID, _ string, cb func(LifecycleObjectRow) error) error {
	for _, r := range f.rows {
		if err := cb(r); err != nil {
			return err
		}
	}
	return nil
}

type fakeSoftDeleter struct {
	mu      sync.Mutex
	deleted []uuid.UUID
}

func (f *fakeSoftDeleter) SoftDelete(_ context.Context, id uuid.UUID, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func TestBuildExpirersDropsRulesWithoutExpiration(t *testing.T) {
	rules := []admindomain.LifecycleRule{
		{ID: "transition-only", Enabled: true, Transition: &admindomain.LifecycleTransition{After: time.Hour}},
		{ID: "valid", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: 24 * time.Hour}},
		{ID: "zero", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: 0}},
	}
	got := buildExpirers(rules, time.Now())
	if len(got) != 1 || got[0].id != "valid" {
		t.Errorf("expected one expirer 'valid', got %+v", got)
	}
}

func TestExpirerMatchesByCommittedAtPreferred(t *testing.T) {
	now := time.Now()
	e := expirer{enabled: true, cutoff: now.Add(-24 * time.Hour)}

	old := now.Add(-25 * time.Hour)
	young := now.Add(-1 * time.Hour)

	if !e.matches(LifecycleObjectRow{CreatedAt: now, CommittedAt: &old}) {
		t.Error("committed_at(old) should win over created_at(now)")
	}
	if e.matches(LifecycleObjectRow{CreatedAt: old, CommittedAt: &young}) {
		t.Error("committed_at(young) should win over created_at(old)")
	}
}

func TestExpirerSkipsDisabled(t *testing.T) {
	e := expirer{enabled: false, cutoff: time.Now().Add(-1 * time.Second)}
	if e.matches(LifecycleObjectRow{CreatedAt: time.Now().Add(-2 * time.Second)}) {
		t.Error("disabled rule must never match")
	}
}

func TestLifecycleWorkerSoftDeletesOnlyAvailableMatches(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	bucket := admindomain.Bucket{
		BackendID:  "primary",
		BucketName: "paladin-archive",
		LifecycleRules: []admindomain.LifecycleRule{{
			ID:         "expire-old",
			Enabled:    true,
			Expiration: &admindomain.LifecycleExpiration{After: 24 * time.Hour},
		}},
	}
	src := &fakeBucketSource{
		buckets: []admindomain.Bucket{bucket},
		bindings: map[string][]ObjectKeyBinding{
			"primary/paladin-archive": {{TenantID: tenantID, ObjectKey: "k"}},
		},
	}

	idOld := uuid.Must(uuid.NewV7())
	idYoung := uuid.Must(uuid.NewV7())
	idDeleted := uuid.Must(uuid.NewV7())
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: idOld, State: string(statemachine.StateAvailable), CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ObjectID: idYoung, State: string(statemachine.StateAvailable), CreatedAt: time.Now().Add(-1 * time.Hour)},
		{ObjectID: idDeleted, State: string(statemachine.StateDeleted), CreatedAt: time.Now().Add(-72 * time.Hour)},
	}}
	sd := &fakeSoftDeleter{}

	w := &LifecycleWorker{
		Buckets:     src,
		Objects:     objs,
		SoftDeleter: sd,
		Now:         time.Now,
	}
	w.tick(context.Background())

	if len(sd.deleted) != 1 || sd.deleted[0] != idOld {
		t.Errorf("expected only idOld deleted, got %+v", sd.deleted)
	}
}
