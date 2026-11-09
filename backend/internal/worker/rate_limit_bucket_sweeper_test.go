package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The sweeper's seam is one method and its whole job is one duration, so the
// thing worth holding is that the grace crossing that boundary is still five
// minutes when it arrives. Too small and the job deletes buckets the limiter
// is still weighting; too large and the table grows, and neither shows up as
// an error.

// record never blocks. These workers tick on a millisecond interval in tests,
// so a full channel on a buffered send would stall the tick — and RunTicker
// only re-checks ctx between ticks, so the worker would never return and the
// test's own cleanup would deadlock rather than fail. Dropping a sample the
// test is not waiting for costs nothing; blocking costs the whole run.
func record[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

type fakeRateSweeper struct {
	called chan time.Duration // the olderThan it was handed
	err    error
}

func newFakeRateSweeper(err error) *fakeRateSweeper {
	return &fakeRateSweeper{called: make(chan time.Duration, 8), err: err}
}

func (f *fakeRateSweeper) SweepRateLimitBuckets(_ context.Context, olderThan time.Duration) (int64, error) {
	record(f.called, olderThan)
	if f.err != nil {
		return 0, f.err
	}
	return 3, nil
}

// runUntilCalled starts Run, waits for the seam to be exercised, then stops it.
// A channel rather than a sleep: the tick either happens and the test proceeds,
// or it does not and the test fails on its own deadline, instead of passing or
// failing by how loaded the machine is.
func runUntilCalled[T any](t *testing.T, run func(context.Context) error, called <-chan T) T {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = run(ctx) }()
	defer func() { cancel(); <-done }()

	select {
	case v := <-called:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never reached its store")
		panic("unreachable")
	}
}

func TestRateLimitBucketSweeper_PassesFiveMinutes(t *testing.T) {
	store := newFakeRateSweeper(nil)
	s := &RateLimitBucketSweeper{Store: store, Interval: time.Millisecond}

	got := runUntilCalled(t, s.Run, store.called)

	if want := 5 * time.Minute; got != want {
		t.Errorf("olderThan = %v, want %v", got, want)
	}
}

func TestRateLimitBucketSweeper_Disabled(t *testing.T) {
	t.Parallel()
	cases := map[string]*RateLimitBucketSweeper{
		"zero interval":     {Store: newFakeRateSweeper(nil), Interval: 0},
		"negative interval": {Store: newFakeRateSweeper(nil), Interval: -time.Second},
		// Not just "does not sweep": a nil store with a live interval would
		// panic on the first tick, which is a crash loop rather than a disabled
		// job. Every other housekeeping worker treats it as off.
		"no store": {Store: nil, Interval: time.Millisecond},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Run(context.Background()); err != nil {
				t.Fatalf("a disabled sweeper must return nil, got %v", err)
			}
		})
	}
}

func TestRateLimitBucketSweeper_KeepsTickingAfterAStoreError(t *testing.T) {
	// A transient database error must not end the loop: the table keeps growing
	// while the worker is gone, and nothing restarts it short of a pod restart.
	store := newFakeRateSweeper(errors.New("deadlock detected"))
	s := &RateLimitBucketSweeper{Store: store, Interval: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Run(ctx) }()
	defer func() { cancel(); <-done }()

	for i := 0; i < 2; i++ {
		select {
		case <-store.called:
		case <-time.After(5 * time.Second):
			t.Fatalf("the sweeper stopped after %d failed tick(s)", i)
		}
	}
}
