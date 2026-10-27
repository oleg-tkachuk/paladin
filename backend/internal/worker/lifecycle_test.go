package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

type fakeBucketSource struct {
	buckets  []admindomain.Bucket
	bindings map[string][]CollectionBinding
	listErr  error
}

func (f *fakeBucketSource) ListBucketsWithLifecycle(_ context.Context) ([]admindomain.Bucket, error) {
	return f.buckets, f.listErr
}
func (f *fakeBucketSource) ListCollectionBindings(_ context.Context, backend, bucket string) ([]CollectionBinding, error) {
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
	mu       sync.Mutex
	deleted  []uuid.UUID
	attempts int
	err      error // returned by every SoftDelete when set
}

func (f *fakeSoftDeleter) SoftDelete(_ context.Context, id uuid.UUID, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func TestBuildExpirersDropsRulesWithoutExpiration(t *testing.T) {
	rules := []admindomain.LifecycleRule{
		{ID: "transition-only", Enabled: true, Transition: &admindomain.LifecycleTransition{After: time.Hour}},
		{ID: "valid", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: 24 * time.Hour}},
		{ID: "zero", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: 0}},
	}
	got, _ := buildExpirers(rules, time.Now(), nil, zap.NewNop())
	if len(got) != 1 || got[0].id != "valid" {
		t.Errorf("expected one expirer 'valid', got %+v", got)
	}
}

func TestExpirerMatchesByCommittedAtPreferred(t *testing.T) {
	now := time.Now()
	e := expirer{enabled: true, cutoff: now.Add(-24 * time.Hour)}

	old := now.Add(-25 * time.Hour)
	young := now.Add(-1 * time.Hour)

	if ok, _ := e.matches(LifecycleObjectRow{CreatedAt: now, CommittedAt: &old}); !ok {
		t.Error("committed_at(old) should win over created_at(now)")
	}
	if ok, _ := e.matches(LifecycleObjectRow{CreatedAt: old, CommittedAt: &young}); ok {
		t.Error("committed_at(young) should win over created_at(old)")
	}
}

func TestExpirerSkipsDisabled(t *testing.T) {
	e := expirer{enabled: false, cutoff: time.Now().Add(-1 * time.Second)}
	if ok, _ := e.matches(LifecycleObjectRow{CreatedAt: time.Now().Add(-2 * time.Second)}); ok {
		t.Error("disabled rule must never match")
	}
}

func TestExpirerCELMatchGatesExpiration(t *testing.T) {
	eval := cel.NewEvaluator()
	now := time.Now()
	rules := []admindomain.LifecycleRule{{
		ID:         "archive-only",
		Enabled:    true,
		Match:      `tags["archive"] == "true"`,
		Expiration: &admindomain.LifecycleExpiration{After: 1 * time.Hour},
	}}
	expirers, _ := buildExpirers(rules, now, eval, zap.NewNop())
	if len(expirers) != 1 {
		t.Fatalf("expected 1 expirer, got %d", len(expirers))
	}

	old := now.Add(-2 * time.Hour) // past cutoff
	rowMatch := LifecycleObjectRow{
		CreatedAt: old,
		Tags:      map[string]string{"archive": "true"},
	}
	rowMiss := LifecycleObjectRow{
		CreatedAt: old,
		Tags:      map[string]string{"archive": "false"},
	}
	if ok, err := expirers[0].matches(rowMatch); err != nil || !ok {
		t.Errorf("archive=true row past cutoff should match: ok=%v err=%v", ok, err)
	}
	if ok, err := expirers[0].matches(rowMiss); err != nil || ok {
		t.Errorf("archive=false row should not match: ok=%v err=%v", ok, err)
	}
}

func TestExpirerMalformedCELMatchSkipsRule(t *testing.T) {
	eval := cel.NewEvaluator()
	rules := []admindomain.LifecycleRule{{
		ID:         "bad-match",
		Enabled:    true,
		Match:      `not a valid expression !@#`,
		Expiration: &admindomain.LifecycleExpiration{After: 1 * time.Hour},
	}}
	got, _ := buildExpirers(rules, time.Now(), eval, zap.NewNop())
	if len(got) != 0 {
		t.Errorf("malformed match should drop the rule, got %d expirers", len(got))
	}
}

func TestExpirerMatchWithoutEvaluatorSkipped(t *testing.T) {
	rules := []admindomain.LifecycleRule{{
		ID:         "needs-eval",
		Enabled:    true,
		Match:      `state == "AVAILABLE"`,
		Expiration: &admindomain.LifecycleExpiration{After: 1 * time.Hour},
	}}
	// Pass nil evaluator — rule with non-empty Match must be skipped
	// (fail-closed) rather than degrading to time-only.
	got, _ := buildExpirers(rules, time.Now(), nil, zap.NewNop())
	if len(got) != 0 {
		t.Errorf("rule with match but no evaluator should be dropped, got %d", len(got))
	}
}

func TestLifecycleWorkerSoftDeletesOnlyAvailableMatches(t *testing.T) {
	tenantID := uuid.NewV7()
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
		bindings: map[string][]CollectionBinding{
			"primary/paladin-archive": {{TenantID: tenantID, Collection: "k"}},
		},
	}

	idOld := uuid.NewV7()
	idYoung := uuid.NewV7()
	idDeleted := uuid.NewV7()
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
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}

	if len(sd.deleted) != 1 || sd.deleted[0] != idOld {
		t.Errorf("expected only idOld deleted, got %+v", sd.deleted)
	}
}

// The rule everyone writes first — expire under a prefix — failed to
// evaluate on every object while the projection lacked `key`, so it expired
// nothing and logged a warning per object.
func TestExpirerMatchesByKeyAndUpdatedAt(t *testing.T) {
	eval := cel.NewEvaluator()
	now := time.Now()
	rules := []admindomain.LifecycleRule{{
		ID:         "logs",
		Enabled:    true,
		Match:      `key.startsWith("logs/") && updated_at < timestamp("2100-01-01T00:00:00Z")`,
		Expiration: &admindomain.LifecycleExpiration{After: 1 * time.Hour},
	}}
	expirers, _ := buildExpirers(rules, now, eval, zap.NewNop())
	if len(expirers) != 1 {
		t.Fatalf("expected 1 expirer, got %d", len(expirers))
	}
	old := now.Add(-2 * time.Hour)
	if ok, err := expirers[0].matches(LifecycleObjectRow{Key: "logs/a", CreatedAt: old, UpdatedAt: old}); err != nil || !ok {
		t.Errorf("logs/ row past cutoff should match: ok=%v err=%v", ok, err)
	}
	if ok, err := expirers[0].matches(LifecycleObjectRow{Key: "data/a", CreatedAt: old, UpdatedAt: old}); err != nil || ok {
		t.Errorf("data/ row should not match: ok=%v err=%v", ok, err)
	}
}

// The rule's expiration window, and the age of objects well past it.
const (
	lifecycleTestWindow = 24 * time.Hour
	lifecycleTestAge    = 2 * lifecycleTestWindow
)

// expiringFixture is one bucket with one binding holding two objects past
// the rule's window, so a tick tries to expire both.
func expiringFixture() (*fakeBucketSource, *fakeObjIter, CollectionBinding, admindomain.Bucket) {
	bind := CollectionBinding{TenantID: uuid.NewV7(), Collection: "logs"}
	bucket := admindomain.Bucket{
		BackendID:  "primary",
		BucketName: "paladin-archive",
		LifecycleRules: []admindomain.LifecycleRule{{
			ID:         "expire-old",
			Enabled:    true,
			Expiration: &admindomain.LifecycleExpiration{After: lifecycleTestWindow},
		}},
	}
	src := &fakeBucketSource{
		buckets:  []admindomain.Bucket{bucket},
		bindings: map[string][]CollectionBinding{bucket.BackendID + "/" + bucket.BucketName: {bind}},
	}
	old := time.Now().Add(-lifecycleTestAge)
	objs := &fakeObjIter{rows: []LifecycleObjectRow{
		{ObjectID: uuid.NewV7(), Key: "logs/a", State: string(statemachine.StateAvailable), CreatedAt: old},
		{ObjectID: uuid.NewV7(), Key: "logs/b", State: string(statemachine.StateAvailable), CreatedAt: old},
	}}
	return src, objs, bind, bucket
}

// A soft-delete the state machine refuses fails the tick, and the other
// objects are still tried: one stuck row must not stall expiry for the rest.
func TestLifecycleWorkerTickReturnsSoftDeleteFailure(t *testing.T) {
	src, objs, _, _ := expiringFixture()
	sd := &fakeSoftDeleter{err: errors.New("state machine unavailable")}
	w := &LifecycleWorker{Buckets: src, Objects: objs, SoftDeleter: sd, Now: time.Now}

	if err := w.tick(context.Background()); !errors.Is(err, sd.err) {
		t.Errorf("tick = %v, want %v", err, sd.err)
	}
	if sd.attempts != len(objs.rows) {
		t.Errorf("soft-delete attempts = %d, want %d: a failure must not stop the rest", sd.attempts, len(objs.rows))
	}
}

func TestLifecycleWorkerTickReturnsListFailure(t *testing.T) {
	src := &fakeBucketSource{listErr: errors.New("bucket store unavailable")}
	w := &LifecycleWorker{Buckets: src, Now: time.Now}
	if err := w.tick(context.Background()); !errors.Is(err, src.listErr) {
		t.Errorf("tick = %v, want %v", err, src.listErr)
	}
}

// A per-object line names where the object lives: without tenant, bucket and
// collection an operator cannot find the object an object_id refers to.
func TestLifecycleWorkerSoftDeleteFailureNamesTheObject(t *testing.T) {
	src, objs, bind, bucket := expiringFixture()
	core, logs := observer.New(zapcore.WarnLevel)
	w := &LifecycleWorker{
		Buckets: src, Objects: objs, Now: time.Now, Logger: zap.New(core),
		SoftDeleter: &fakeSoftDeleter{err: errors.New("state machine unavailable")},
	}
	_ = w.tick(context.Background()) // fails by design; the log is what this test reads

	lines := logs.FilterMessage("failed to soft-delete object").All()
	if len(lines) != len(objs.rows) {
		t.Fatalf("soft-delete failure lines = %d, want %d", len(lines), len(objs.rows))
	}
	want := map[string]string{
		logKeyTenant:     bind.TenantID.String(),
		logKeyCollection: bind.Collection,
		logKeyBackend:    bucket.BackendID,
		logKeyBucket:     bucket.BucketName,
		logKeyObjectID:   objs.rows[0].ObjectID.String(),
		logKeyKey:        objs.rows[0].Key,
	}
	assertLogFields(t, lines[0], want)
}

// assertLogFields checks that entry carries every key in want with its value.
func assertLogFields(t *testing.T, entry observer.LoggedEntry, want map[string]string) {
	t.Helper()
	got := entry.ContextMap()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: field %q = %v, want %q", entry.Message, k, got[k], v)
		}
	}
}

// A rule that cannot run is skipped and reported: the tick returns it, so the
// failing-worker alert sees a bucket whose expiry stopped.
func TestBuildExpirersReportsARuleItSkips(t *testing.T) {
	const keptRule, brokenRule = "kept", "broken"
	week := 7 * 24 * time.Hour
	rules := []admindomain.LifecycleRule{
		{ID: keptRule, Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: week}},
		{ID: brokenRule, Enabled: true, Match: "size_bytes >", Expiration: &admindomain.LifecycleExpiration{After: week}},
	}
	got, err := buildExpirers(rules, time.Now(), nil, zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), brokenRule) {
		t.Fatalf("err = %v, want it to name %s", err, brokenRule)
	}
	if len(got) != 1 || got[0].id != keptRule {
		t.Errorf("expirers = %+v, want only %s", got, keptRule)
	}
	if _, err := buildExpirers(rules[:1], time.Now(), nil, zap.NewNop()); err != nil {
		t.Errorf("a runnable rule reported %v", err)
	}
}
