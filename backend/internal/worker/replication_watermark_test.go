package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// fakeWatermarkStore is an in-memory WatermarkStore stub that records every
// Advance call so tests can assert ordering and idempotency.
type fakeWatermarkStore struct {
	mu        sync.Mutex
	stored    map[string]time.Time
	advances  []time.Time
	advanceFn func(time.Time) error
	getErr    error
}

func newFakeWatermarkStore() *fakeWatermarkStore {
	return &fakeWatermarkStore{stored: map[string]time.Time{}}
}

func (f *fakeWatermarkStore) Get(_ context.Context, backendID, bucketName string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stored[backendID+"/"+bucketName], f.getErr
}

func (f *fakeWatermarkStore) Advance(_ context.Context, backendID, bucketName string, t time.Time) error {
	if f.advanceFn != nil {
		if err := f.advanceFn(t); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advances = append(f.advances, t)
	cur := f.stored[backendID+"/"+bucketName]
	if t.After(cur) {
		f.stored[backendID+"/"+bucketName] = t
	}
	return nil
}

func TestReplicationCutoffPrefersStoreOverLookback(t *testing.T) {
	store := newFakeWatermarkStore()
	stored := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	_ = store.Advance(context.Background(), "primary", "paladin-archive", stored)

	w := &ReplicationWorker{
		Watermarks:     store,
		LookbackWindow: 1 * time.Hour,
		Now:            func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) },
		watermarks:     map[string]time.Time{},
	}
	got, err := w.cutoff(context.Background(), "primary", "paladin-archive", w.Now())
	if err != nil {
		t.Fatalf("cutoff: %v", err)
	}
	if !got.Equal(stored) {
		t.Errorf("cutoff: got %v want stored %v", got, stored)
	}
}

func TestReplicationCutoffFallsBackToLookbackWhenStoreEmpty(t *testing.T) {
	now := time.Now()
	w := &ReplicationWorker{
		Watermarks:     newFakeWatermarkStore(),
		LookbackWindow: 1 * time.Hour,
		Now:            func() time.Time { return now },
		watermarks:     map[string]time.Time{},
	}
	got, err := w.cutoff(context.Background(), "primary", "paladin-archive", w.Now())
	if err != nil {
		t.Fatalf("cutoff: %v", err)
	}
	want := now.Add(-1 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("cutoff: got %v want %v", got, want)
	}
	// The window's start is written down at once, so it does not slide.
	if stored := w.Watermarks.(*fakeWatermarkStore).stored["primary/paladin-archive"]; !stored.Equal(want) {
		t.Errorf("stored watermark = %v, want the window's start %v", stored, want)
	}
}

func TestReplicationAdvanceMonotonic(t *testing.T) {
	store := newFakeWatermarkStore()
	w := &ReplicationWorker{
		Watermarks: store,
		watermarks: map[string]time.Time{},
		Now:        time.Now,
	}
	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Hour)
	t3 := t1.Add(-30 * time.Minute) // older than t1, must not advance

	if err := w.advance(context.Background(), "primary", "paladin-archive", t1); err != nil {
		t.Fatalf("advance(t1) = %v", err)
	}
	if err := w.advance(context.Background(), "primary", "paladin-archive", t2); err != nil {
		t.Fatalf("advance(t2) = %v", err)
	}
	if err := w.advance(context.Background(), "primary", "paladin-archive", t3); err != nil {
		t.Fatalf("advance(t3) = %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.advances) != 2 {
		t.Errorf("expected 2 store writes (t1, t2 only), got %d", len(store.advances))
	}
	if got := store.stored["primary/paladin-archive"]; !got.Equal(t2) {
		t.Errorf("stored watermark: got %v want %v", got, t2)
	}
}

func TestReplicationAdvanceCachesInMemory(t *testing.T) {
	store := newFakeWatermarkStore()
	w := &ReplicationWorker{
		Watermarks: store,
		watermarks: map[string]time.Time{},
		Now:        time.Now,
	}
	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := w.advance(context.Background(), "primary", "paladin-archive", t1); err != nil {
		t.Fatalf("advance(t1) = %v", err)
	}

	// Same Get call should hit the in-memory cache, not roundtrip to store.
	// We assert this indirectly by verifying the cached value matches t1.
	if got, ok := w.watermarks["primary/paladin-archive"]; !ok || !got.Equal(t1) {
		t.Errorf("in-memory cache miss: got %v ok=%v want %v", got, ok, t1)
	}
}

// A watermark that could not be read defers the bucket rather than falling
// back to the lookback window: a window starting after the stored watermark
// would let the pass move the watermark past objects it never selected.
func TestReplicationCutoffRefusesAnUnreadableWatermark(t *testing.T) {
	now := time.Now()
	store := newFakeWatermarkStore()
	store.getErr = errors.New("pool closed")
	core, logs := observer.New(zap.WarnLevel)
	w := &ReplicationWorker{
		Watermarks:     store,
		LookbackWindow: time.Hour,
		Now:            func() time.Time { return now },
		Logger:         zap.New(core),
		watermarks:     map[string]time.Time{},
	}
	if _, err := w.cutoff(context.Background(), "primary", "paladin-archive", w.Now()); !errors.Is(err, store.getErr) {
		t.Errorf("cutoff error = %v, want %v", err, store.getErr)
	}
	got := logs.All()
	if len(got) != 1 || got[0].ContextMap()[logKeyBucket] != "paladin-archive" {
		t.Errorf("logged %v, want one line naming the bucket", got)
	}
}
