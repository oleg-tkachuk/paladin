package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// scriptedReplicator answers each copy by the object's key: the errors listed
// for a key are returned one per attempt, then the copy succeeds.
type scriptedReplicator struct {
	mu       sync.Mutex
	script   map[string][]error
	attempts map[string]int
	copied   []replicatedCall
}

type replicatedCall struct {
	src, dst ReplicationTarget
	size     int64
}

func newScriptedReplicator(script map[string][]error) *scriptedReplicator {
	return &scriptedReplicator{script: script, attempts: map[string]int{}}
}

func (s *scriptedReplicator) Replicate(_ context.Context, src, dst ReplicationTarget, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.attempts[src.Key]
	s.attempts[src.Key] = n + 1
	if errs := s.script[src.Key]; n < len(errs) && errs[n] != nil {
		return errs[n]
	}
	s.copied = append(s.copied, replicatedCall{src: src, dst: dst, size: size})
	return nil
}

func (s *scriptedReplicator) copiedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.copied))
	for _, c := range s.copied {
		out = append(out, c.src.Key)
	}
	return out
}

// repeat is n copies of err, one per attempt.
func repeat(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}

var (
	errTransientCopy = fmt.Errorf("slow down: %w", ErrCopyTransient)
	errPermanentCopy = errors.New("access denied")
	errSourceGone    = fmt.Errorf("no such key: %w", ErrObjectMissing)
)

// passFixture is a replicating bucket whose objects are given newest first,
// as the iterator yields them, each committed age before now.
type passFixture struct {
	now    time.Time
	store  *fakeWatermarkStore
	sleeps []time.Duration
}

const (
	passBackend = "primary"
	passBucket  = "paladin-archive"
	passSettle  = time.Minute
)

func newPassFixture() *passFixture {
	return &passFixture{now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), store: newFakeWatermarkStore()}
}

// row is an AVAILABLE object with the given key, committed age before now.
func (f *passFixture) row(key string, age time.Duration) LifecycleObjectRow {
	committed := f.now.Add(-age)
	return LifecycleObjectRow{ObjectID: uuid.NewV7(), Key: key, State: "AVAILABLE", CommittedAt: &committed, SizeBytes: int64(len(key))}
}

func (f *passFixture) worker(rows []LifecycleObjectRow, rep StorageReplicator, logger *zap.Logger) *ReplicationWorker {
	src, _, _, _ := replicatingFixture(f.now)
	return &ReplicationWorker{
		Buckets:        src,
		Objects:        &fakeObjIter{rows: rows},
		Replicator:     rep,
		Watermarks:     f.store,
		LookbackWindow: replicationTestLookback,
		SettleDelay:    passSettle,
		Now:            func() time.Time { return f.now },
		Logger:         logger,
		sleep: func(_ context.Context, d time.Duration) error {
			f.sleeps = append(f.sleeps, d)
			return nil
		},
		watermarks: map[string]time.Time{},
	}
}

func (f *passFixture) horizon() time.Time { return f.now.Add(-passSettle) }

func (f *passFixture) stored() time.Time {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return f.store.stored[passBackend+"/"+passBucket]
}

// Each copy carries the object's key to both sides, and its size.
func TestReplicationCopiesTheObjectKeyAndSize(t *testing.T) {
	f := newPassFixture()
	rep := newScriptedReplicator(nil)
	w := f.worker([]LifecycleObjectRow{f.row("docs/report.pdf", 2*passSettle)}, rep, nil)
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if len(rep.copied) != 1 {
		t.Fatalf("copies = %d, want 1", len(rep.copied))
	}
	c := rep.copied[0]
	if c.src.Key != "docs/report.pdf" || c.dst.Key != "docs/report.pdf" {
		t.Errorf("keys = (%q, %q), want the row's key on both sides", c.src.Key, c.dst.Key)
	}
	if c.size != int64(len("docs/report.pdf")) {
		t.Errorf("size = %d, want the row's", c.size)
	}
}

// A completed pass moves the watermark to the horizon, not to the newest copy.
func TestReplicationAdvancesTheWatermarkToTheHorizon(t *testing.T) {
	f := newPassFixture()
	w := f.worker([]LifecycleObjectRow{f.row("a", 3*passSettle)}, newScriptedReplicator(nil), nil)
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if got := f.stored(); !got.Equal(f.horizon()) {
		t.Errorf("watermark = %v, want the horizon %v", got, f.horizon())
	}
}

// An object committed inside the settle delay is left for a later tick, and
// the watermark stays below it, so that tick still selects it.
func TestReplicationLeavesAnObjectInsideTheSettleDelay(t *testing.T) {
	f := newPassFixture()
	recent := f.row("recent", passSettle/2)
	rep := newScriptedReplicator(nil)
	w := f.worker([]LifecycleObjectRow{recent}, rep, nil)
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if got := rep.copiedKeys(); len(got) != 0 {
		t.Errorf("copied %v, want nothing inside the settle delay", got)
	}
	if !recent.CommittedAt.After(f.stored()) {
		t.Errorf("watermark %v passed an object committed at %v that was never copied", f.stored(), *recent.CommittedAt)
	}
}

// The heart of the watermark: an older object that failed holds it, though a
// newer one copied — the next tick must still select the failed object.
func TestReplicationHoldsTheWatermarkBelowAFailedObject(t *testing.T) {
	f := newPassFixture()
	rows := []LifecycleObjectRow{f.row("newer", 2*passSettle), f.row("older", 3*passSettle)}
	rep := newScriptedReplicator(map[string][]error{"older": {errPermanentCopy}})
	w := f.worker(rows, rep, nil)

	if err := w.tick(context.Background()); !errors.Is(err, errPermanentCopy) {
		t.Fatalf("tick = %v, want %v", err, errPermanentCopy)
	}
	if got := f.stored(); !got.IsZero() {
		t.Fatalf("watermark advanced to %v past an object that failed to copy", got)
	}
	// The failure clears; the next tick copies it and only then advances.
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("second tick = %v", err)
	}
	if rep.attempts["older"] != 2 {
		t.Errorf("older attempted %d times, want 2: the next tick must select it again", rep.attempts["older"])
	}
	if got := f.stored(); !got.Equal(f.horizon()) {
		t.Errorf("watermark = %v, want the horizon once everything copied", got)
	}
}

// A permanent failure is not retried, and does not stop the objects after it.
func TestReplicationDoesNotRetryAPermanentFailure(t *testing.T) {
	f := newPassFixture()
	rows := []LifecycleObjectRow{f.row("bad", 2*passSettle), f.row("good", 3*passSettle)}
	rep := newScriptedReplicator(map[string][]error{"bad": repeat(errPermanentCopy, replicationCopyAttempts)})
	w := f.worker(rows, rep, nil)

	_ = w.tick(context.Background())
	if rep.attempts["bad"] != 1 {
		t.Errorf("a permanent failure was tried %d times, want 1", rep.attempts["bad"])
	}
	if len(f.sleeps) != 0 {
		t.Errorf("waited %v before a retry of a permanent failure", f.sleeps)
	}
	if got := rep.copiedKeys(); len(got) != 1 || got[0] != "good" {
		t.Errorf("copied %v, want the object after the failure too", got)
	}
}

// A transient failure is retried with exponential backoff, and a copy that
// succeeds within the attempts counts as copied.
func TestReplicationRetriesATransientFailureWithBackoff(t *testing.T) {
	f := newPassFixture()
	failures := replicationCopyAttempts - 1
	rep := newScriptedReplicator(map[string][]error{"a": repeat(errTransientCopy, failures)})
	w := f.worker([]LifecycleObjectRow{f.row("a", 2*passSettle)}, rep, nil)

	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if rep.attempts["a"] != replicationCopyAttempts {
		t.Errorf("attempts = %d, want %d", rep.attempts["a"], replicationCopyAttempts)
	}
	want := []time.Duration{replicationBackoffBase, 2 * replicationBackoffBase, 4 * replicationBackoffBase}
	if fmt.Sprint(f.sleeps) != fmt.Sprint(want) {
		t.Errorf("backoff = %v, want %v", f.sleeps, want)
	}
	if got := f.stored(); !got.Equal(f.horizon()) {
		t.Errorf("watermark = %v, want the horizon", got)
	}
}

// A copy still failing transiently after every attempt stops the bucket for
// the tick — its backend is likely down for the objects after it too — and
// holds the watermark.
func TestReplicationStopsTheBucketWhenRetriesRunOut(t *testing.T) {
	f := newPassFixture()
	rows := []LifecycleObjectRow{f.row("down", 2*passSettle), f.row("after", 3*passSettle)}
	rep := newScriptedReplicator(map[string][]error{"down": repeat(errTransientCopy, replicationCopyAttempts)})
	w := f.worker(rows, rep, nil)

	if err := w.tick(context.Background()); !errors.Is(err, ErrCopyTransient) {
		t.Fatalf("tick = %v, want a transient failure", err)
	}
	if rep.attempts["down"] != replicationCopyAttempts {
		t.Errorf("attempts = %d, want %d", rep.attempts["down"], replicationCopyAttempts)
	}
	if n := rep.attempts["after"]; n != 0 {
		t.Errorf("the object after an exhausted retry was tried %d times; the bucket should stop", n)
	}
	if got := f.stored(); !got.IsZero() {
		t.Errorf("watermark advanced to %v after a stopped pass", got)
	}
}

// A source the backend does not hold is passed over: there is nothing to copy
// now or later, so it does not hold the watermark.
func TestReplicationPassesOverAMissingSource(t *testing.T) {
	f := newPassFixture()
	rep := newScriptedReplicator(map[string][]error{"gone": {errSourceGone}})
	core, logs := observer.New(zapcore.WarnLevel)
	w := f.worker([]LifecycleObjectRow{f.row("gone", 2*passSettle)}, rep, zap.New(core))

	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if got := f.stored(); !got.Equal(f.horizon()) {
		t.Errorf("watermark = %v, want the horizon", got)
	}
	if n := logs.FilterMessage("source object missing; passing over it").Len(); n != 1 {
		t.Errorf("missing-source lines = %d, want 1", n)
	}
}

// A dry run copies nothing, so it never moves the watermark.
func TestReplicationDryRunNeverAdvances(t *testing.T) {
	f := newPassFixture()
	w := f.worker([]LifecycleObjectRow{f.row("a", 2*passSettle)}, nil, nil)
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
	if got := f.stored(); !got.IsZero() {
		t.Errorf("a dry run advanced the watermark to %v", got)
	}
}

// A bucket whose watermark cannot be read copies nothing for the tick.
func TestReplicationDefersABucketWithAnUnreadableWatermark(t *testing.T) {
	f := newPassFixture()
	f.store.getErr = errors.New("pool closed")
	rep := newScriptedReplicator(nil)
	w := f.worker([]LifecycleObjectRow{f.row("a", 2*passSettle)}, rep, nil)
	if err := w.tick(context.Background()); !errors.Is(err, f.store.getErr) {
		t.Errorf("tick = %v, want %v", err, f.store.getErr)
	}
	if got := rep.copiedKeys(); len(got) != 0 {
		t.Errorf("copied %v under an unreadable watermark", got)
	}
}

// A worker stopped mid-backoff reports no failure and advances nothing.
func TestReplicationStopsQuietlyWhenCancelled(t *testing.T) {
	f := newPassFixture()
	ctx, cancel := context.WithCancel(context.Background())
	rep := newScriptedReplicator(map[string][]error{"a": repeat(errTransientCopy, replicationCopyAttempts)})
	w := f.worker([]LifecycleObjectRow{f.row("a", 2*passSettle)}, rep, nil)
	w.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	if err := w.tick(ctx); err != nil {
		t.Errorf("tick = %v, want nil when the worker is stopping", err)
	}
	if got := f.stored(); !got.IsZero() {
		t.Errorf("watermark advanced to %v on a cancelled pass", got)
	}
}

func TestReplicationBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, replicationBackoffBase},
		{2, 2 * replicationBackoffBase},
		{3, 4 * replicationBackoffBase},
		{5, 16 * replicationBackoffBase},
		{6, replicationBackoffMax},
		{50, replicationBackoffMax},
	}
	for _, tc := range cases {
		if got := replicationBackoff(tc.attempt); got != tc.want {
			t.Errorf("replicationBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// The timer wait returns early when the context ends.
func TestReplicationWaitHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &ReplicationWorker{}
	if err := w.wait(ctx, replicationBackoffMax); !errors.Is(err, context.Canceled) {
		t.Errorf("wait = %v, want context.Canceled", err)
	}
	if err := w.wait(context.Background(), time.Nanosecond); err != nil {
		t.Errorf("wait = %v, want nil once the timer fires", err)
	}
}
