package ratelimit

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestNoopLimiter_AllowsAll confirms the zero-cost variant lets every
// call through and never touches a backend. Used by deploys that wire
// the api_token subsystem but don't enable rate-limiting (or by tests
// that want verify-only semantics).
func TestNoopLimiter_AllowsAll(t *testing.T) {
	t.Parallel()
	l := NoopLimiter{}
	for i := 0; i < 1000; i++ {
		d, err := l.Allow(context.Background(), uuid.New(), 1)
		if err != nil {
			t.Fatalf("noop returned err: %v", err)
		}
		if !d.Allowed {
			t.Fatal("noop denied a request")
		}
	}
}

// TestNoopLimiter_SweepReturnsZero confirms Sweep is a no-op so the
// purger doesn't log spurious "0 rows swept" lines.
func TestNoopLimiter_SweepReturnsZero(t *testing.T) {
	t.Parallel()
	n, err := NoopLimiter{}.Sweep(context.Background(), time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("noop sweep: n=%d err=%v", n, err)
	}
}

// recordingLimiter is a stub that lets us drive interceptor tests
// without hitting Postgres. Tracks the (tokenID, capacity) tuples it
// was called with and returns a configurable Decision.
type recordingLimiter struct {
	calls atomic.Int64
	resp  Decision
}

func (r *recordingLimiter) Allow(_ context.Context, _ uuid.UUID, _ int) (Decision, error) {
	r.calls.Add(1)
	return r.resp, nil
}

func (r *recordingLimiter) Sweep(context.Context, time.Duration) (int64, error) { return 0, nil }

// TestRecordingLimiter_RoundTrip is a smoke test for the test helper —
// confirms the interface contract holds against a non-noop impl.
func TestRecordingLimiter_RoundTrip(t *testing.T) {
	t.Parallel()
	l := &recordingLimiter{resp: Decision{Allowed: false, RetryAfter: 30 * time.Second}}
	var lim Limiter = l
	d, err := lim.Allow(context.Background(), uuid.New(), 60)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.Allowed {
		t.Fatal("expected denied")
	}
	if d.RetryAfter != 30*time.Second {
		t.Errorf("retry-after: got %v, want 30s", d.RetryAfter)
	}
	if l.calls.Load() != 1 {
		t.Errorf("calls: got %d, want 1", l.calls.Load())
	}
}
