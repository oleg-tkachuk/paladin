package pgsync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
)

// shared is an in-memory Store standing in for rate_limit_buckets, shared by
// every Counter built on it.
type shared struct {
	mu        sync.Mutex
	buckets   map[string]map[time.Time]int64
	fail      error
	exchanges int
	// during runs inside Exchange, as a request arriving mid-sync would.
	during func()
}

func newShared() *shared { return &shared{buckets: map[string]map[time.Time]int64{}} }

func (s *shared) Exchange(_ context.Context, deltas []Delta, keys []string, since time.Time) ([]Total, error) {
	if s.during != nil {
		s.during()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exchanges++
	if s.fail != nil {
		return nil, s.fail
	}
	for _, d := range deltas {
		if s.buckets[d.Key] == nil {
			s.buckets[d.Key] = map[time.Time]int64{}
		}
		s.buckets[d.Key][d.Start] += d.Count
	}
	var out []Total
	for _, k := range keys {
		for start, n := range s.buckets[k] {
			if !start.Before(since) {
				out = append(out, Total{Key: k, Start: start, Count: n})
			}
		}
	}
	return out, nil
}

var bucketAt = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

const (
	key      = "tenant:test"
	interval = time.Second
)

// replica is a Counter on store at a settable time.
type replica struct {
	*Counter
	t time.Time
}

func newReplica(store Store, log *zap.Logger) *replica {
	r := &replica{Counter: New(store, interval, log), t: bucketAt.Add(10 * time.Second)}
	r.now = func() time.Time { return r.t }
	return r
}

func bump(t *testing.T, r *replica, n int) ratecount.Counts {
	t.Helper()
	var c ratecount.Counts
	for i := 0; i < n; i++ {
		c, _ = r.Bump(context.Background(), key)
	}
	return c
}

func mustSync(t *testing.T, r *replica) {
	t.Helper()
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func TestReplicasShareCountsThroughASync(t *testing.T) {
	store := newShared()
	a, b := newReplica(store, zap.NewNop()), newReplica(store, zap.NewNop())
	bump(t, a, 3)
	if c, _ := b.Read(context.Background(), key); c.Current != 0 {
		t.Fatalf("b saw a's counts before any sync: %+v", c)
	}
	mustSync(t, a)
	mustSync(t, b)
	if c := bump(t, b, 1); c.Current != 4 {
		t.Fatalf("b after both synced: %+v, want 4", c)
	}
	mustSync(t, b)
	mustSync(t, a)
	if c, _ := a.Read(context.Background(), key); c.Current != 4 {
		t.Fatalf("a after b's sync: %+v, want 4", c)
	}
}

// Each replica admits up to the capacity on its own between syncs — the
// overshoot ADR-0046 bounds — and both refuse once they have synced.
func TestOvershootIsBoundedBySync(t *testing.T) {
	const capacity = 5
	store := newShared()
	a, b := newReplica(store, zap.NewNop()), newReplica(store, zap.NewNop())
	admitted := 0
	for _, r := range []*replica{a, b} {
		for i := 0; i < capacity+1; i++ {
			if c, _ := r.BumpIfFits(context.Background(), key, capacity); c.Admitted {
				admitted++
			}
		}
	}
	if admitted != 2*capacity {
		t.Fatalf("admitted %d between syncs, want %d (one capacity per replica)", admitted, 2*capacity)
	}
	mustSync(t, a)
	mustSync(t, b)
	mustSync(t, a)
	for _, r := range []*replica{a, b} {
		if c, _ := r.BumpIfFits(context.Background(), key, capacity); c.Admitted || c.Current != 2*capacity {
			t.Fatalf("after syncing: %+v", c)
		}
	}
}

func TestARequestDuringASyncIsCountedOnce(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 2)
	store.during = func() {
		// Mid-sync, the two in flight still count.
		if c, _ := a.Read(context.Background(), key); c.Current != 2 {
			t.Errorf("read mid-sync: %+v, want the 2 in flight", c)
		}
		bump(t, a, 1)
	}
	mustSync(t, a)
	store.during = nil
	if c, _ := a.Read(context.Background(), key); c.Current != 3 {
		t.Fatalf("after a sync with a request mid-flight: %+v, want 3", c)
	}
	mustSync(t, a)
	if c, _ := a.Read(context.Background(), key); c.Current != 3 {
		t.Fatalf("after the next sync: %+v, want 3", c)
	}
}

func TestAFailedSyncKeepsTheCounts(t *testing.T) {
	store := newShared()
	core, logs := observer.New(zapcore.InfoLevel)
	a := newReplica(store, zap.New(core))
	bump(t, a, 2)
	store.fail = errors.New("down")
	if err := a.Sync(context.Background()); err == nil {
		t.Fatal("a failed exchange reported success")
	}
	if c, _ := a.Read(context.Background(), key); c.Current != 2 {
		t.Fatalf("counts after a failed sync: %+v", c)
	}
	if !a.fallback.Degraded() || logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Fatalf("a failed sync not reported: degraded=%v logs=%v", a.fallback.Degraded(), logs.All())
	}
	store.fail = nil
	mustSync(t, a)
	if got := store.buckets[key][bucketAt]; got != 2 {
		t.Fatalf("shared bucket %d after recovery, want 2", got)
	}
	if a.fallback.Degraded() {
		t.Fatal("still degraded after a sync that worked")
	}
}

func TestTheWindowRollsOver(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 3)
	mustSync(t, a)
	a.t = a.t.Add(ratecount.Window)
	c, _ := a.Read(context.Background(), key)
	if c.Current != 0 || c.Previous != 3 {
		t.Fatalf("next bucket: %+v, want 0 current and 3 previous", c)
	}
	mustSync(t, a)
	if c, _ := a.Read(context.Background(), key); c.Previous != 3 {
		t.Fatalf("previous bucket lost at the sync after a rollover: %+v", c)
	}
}

func TestIdleKeysStopSyncing(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 1)
	mustSync(t, a)
	a.t = a.t.Add(2 * ratecount.Window)
	mustSync(t, a)
	if _, ok := a.keys[key]; ok {
		t.Fatal("a key idle for a whole window is still synced")
	}
	before := store.exchanges
	mustSync(t, a)
	if store.exchanges != before {
		t.Fatal("a sync with no live key reached the store")
	}
}

func TestRunSyncsOnTheWayOut(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	cancel()
	<-done
	if got := store.buckets[key][bucketAt]; got != 1 {
		t.Fatalf("shared bucket %d after Run stopped, want 1", got)
	}
}

func TestBumpIfFitsAndDegradedDecisions(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	if c, _ := a.BumpIfFits(context.Background(), key, 1); !c.Admitted {
		t.Fatal("first call refused")
	}
	if c, _ := a.BumpIfFits(context.Background(), key, 1); c.Admitted {
		t.Fatal("a capacity of one admitted two")
	}
	c, _ := a.Bump(context.Background(), key)
	if !c.Admitted || c.Current != 2 || !c.BucketStart.Equal(bucketAt) || c.Elapsed != 10*time.Second {
		t.Fatalf("bump: %+v", c)
	}
}

// A key last touched in the bucket that just became the previous one is
// still in the window: the sync keeps it and sends what it counted there.
func TestASyncAfterARolloverKeepsThePreviousBucket(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 3)
	a.t = a.t.Add(ratecount.Window)
	mustSync(t, a)
	if got := store.buckets[key][bucketAt]; got != 3 {
		t.Fatalf("shared previous bucket %d, want the 3 counted before the rollover", got)
	}
	if c, _ := a.Read(context.Background(), key); c.Previous != 3 {
		t.Fatalf("after the sync: %+v, want 3 previous", c)
	}
}

func TestAFailedSyncAfterARolloverKeepsThePreviousBucket(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 2)
	a.t = a.t.Add(ratecount.Window)
	store.fail = errors.New("down")
	_ = a.Sync(context.Background())
	store.fail = nil
	mustSync(t, a)
	if got := store.buckets[key][bucketAt]; got != 2 {
		t.Fatalf("shared previous bucket %d after a failed then a good sync, want 2", got)
	}
}

// Counts of a bucket that has left the window are weighed by nobody, so a
// sync does not write them.
func TestASyncSkipsBucketsOutsideTheWindow(t *testing.T) {
	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 1)
	a.t = a.t.Add(2 * ratecount.Window)
	bump(t, a, 1)
	mustSync(t, a)
	if _, ok := store.buckets[key][bucketAt]; ok {
		t.Fatal("a bucket two windows old was written")
	}
}

func TestCallsDecidedWhileDegradedAreCounted(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	store := newShared()
	a := newReplica(store, zap.NewNop())
	bump(t, a, 1) // healthy: not counted
	store.fail = errors.New("down")
	_ = a.Sync(context.Background())
	bump(t, a, 2)
	_, _ = a.BumpIfFits(context.Background(), key, 100)
	_, _ = a.Read(context.Background(), key) // a read decides nothing

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "paladin.ratelimit.degraded" {
				continue
			}
			pts := m.Data.(metricdata.Sum[int64]).DataPoints
			if len(pts) != 1 || pts[0].Value != 3 {
				t.Fatalf("degraded points %+v, want 3 decisions", pts)
			}
			return
		}
	}
	t.Fatal("no degraded metric recorded")
}
