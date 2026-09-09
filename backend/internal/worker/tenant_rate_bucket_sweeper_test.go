package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The sweeper had no test. Its seam is one method and its whole job is a
// number with a unit attached — `olderThanMicros`, per the interface's own
// comment — so the thing worth holding is that the grace crossing that
// boundary is still five minutes when it arrives. Get the unit wrong and the
// job either deletes buckets the limiter is still weighting (too small) or
// never deletes anything and the table grows forever (too large), and neither
// shows up as an error.

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
	called chan int64 // the olderThanMicros it was handed
	err    error
}

func newFakeRateSweeper(err error) *fakeRateSweeper {
	return &fakeRateSweeper{called: make(chan int64, 8), err: err}
}

func (f *fakeRateSweeper) SweepTenantRateBuckets(_ context.Context, olderThanMicros int64) (int64, error) {
	record(f.called, olderThanMicros)
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

func TestTenantRateBucketSweeper_PassesFiveMinutesInMicroseconds(t *testing.T) {
	store := newFakeRateSweeper(nil)
	s := &TenantRateBucketSweeper{Store: store, Interval: time.Millisecond}

	got := runUntilCalled(t, s.Run, store.called)

	if want := (5 * time.Minute).Microseconds(); got != want {
		t.Errorf("olderThanMicros = %d, want %d (five minutes); the boundary "+
			"takes microseconds, so a duration passed raw is off by 10^6", got, want)
	}
}

func TestTenantRateBucketSweeper_Disabled(t *testing.T) {
	t.Parallel()
	cases := map[string]*TenantRateBucketSweeper{
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

func TestTenantRateBucketSweeper_KeepsTickingAfterAStoreError(t *testing.T) {
	// A transient database error must not end the loop: the table keeps growing
	// while the worker is gone, and nothing restarts it short of a pod restart.
	store := newFakeRateSweeper(errors.New("deadlock detected"))
	s := &TenantRateBucketSweeper{Store: store, Interval: time.Millisecond}

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
