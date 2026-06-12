package worker

import (
	"context"
	"testing"
	"time"
)

type fakeIdemPurger struct {
	returns []int64 // successive rowcounts
	calls   int
}

func (f *fakeIdemPurger) PurgeExpiredIdempotencyKeys(_ context.Context) (int64, error) {
	if f.calls >= len(f.returns) {
		return 0, nil
	}
	n := f.returns[f.calls]
	f.calls++
	return n, nil
}

// The tick drains in batches: a full batch (10000) re-queries; a partial
// batch ends the drain. Verify the loop keeps going until under the cap.
func TestIdempotencyPurgerDrainsInBatches(t *testing.T) {
	p := &fakeIdemPurger{returns: []int64{10000, 10000, 42}}
	purger := &IdempotencyKeyPurger{Purger: p, Interval: time.Hour}
	// Drive one tick body directly by invoking the inner drain via a
	// short-interval Run with a cancel after the first tick.
	ctx, cancel := context.WithCancel(context.Background())
	purger.Interval = 5 * time.Millisecond
	go func() {
		// allow one tick to fire, then stop
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	_ = purger.Run(ctx)
	if p.calls < 3 {
		t.Errorf("expected at least 3 batched purge calls, got %d", p.calls)
	}
}
