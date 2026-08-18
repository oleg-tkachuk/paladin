package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeQuotaStore struct {
	mu        sync.Mutex
	reconcile func() (int64, error)
	roll      func(time.Time) (int64, error)
	calls     int
	dayStarts []time.Time
}

func (f *fakeQuotaStore) ReconcileUsage(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.reconcile == nil {
		return 0, nil
	}
	return f.reconcile()
}

func (f *fakeQuotaStore) RollDailyCounters(_ context.Context, dayStart time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dayStarts = append(f.dayStarts, dayStart)
	if f.roll == nil {
		return 0, nil
	}
	return f.roll(dayStart)
}

func (f *fakeQuotaStore) snapshot() (int, []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]time.Time(nil), f.dayStarts...)
}

// A non-positive interval disables the job, matching every other worker.
// Run must return immediately rather than spinning or blocking on a ticker.
func TestQuotaReconciler_DisabledByZeroInterval(t *testing.T) {
	store := &fakeQuotaStore{}
	q := &QuotaReconciler{Store: store, Interval: 0}

	done := make(chan error, 1)
	go func() { done <- q.Run(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil when disabled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run blocked with a zero interval; want immediate return")
	}
	if calls, _ := store.snapshot(); calls != 0 {
		t.Errorf("store called %d times while disabled, want 0", calls)
	}
}

// Both halves run on every tick, and the day boundary handed to the roll is
// truncated to UTC midnight — that truncation is what makes the roll
// idempotent across ticks and across pods.
func TestQuotaReconciler_TickRunsBothHalvesAtUTCMidnight(t *testing.T) {
	store := &fakeQuotaStore{}
	q := &QuotaReconciler{Store: store, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = q.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		calls, days := store.snapshot()
		if calls > 0 && len(days) > 0 {
			for _, d := range days {
				if !d.Equal(d.Truncate(24 * time.Hour)) {
					t.Errorf("day start %v is not truncated to UTC midnight", d)
				}
				if d.Location() != time.UTC {
					t.Errorf("day start %v is not in UTC", d)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no tick observed: reconcile=%d roll=%d", calls, len(days))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// A failing reconcile must not skip the daily roll: the two halves fix
// different bugs and one being broken is no reason to leave per-day caps
// stuck as lifetime caps.
func TestQuotaReconciler_RollRunsEvenWhenReconcileFails(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeQuotaStore{
		reconcile: func() (int64, error) { return 0, boom },
	}
	q := &QuotaReconciler{Store: store, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = q.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if _, days := store.snapshot(); len(days) > 0 {
			return // roll happened despite the reconcile error
		}
		select {
		case <-deadline:
			t.Fatal("daily roll never ran after a reconcile failure")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// The loop survives errors — a failed tick is reported for metrics but must
// not terminate the worker, or one transient DB blip would silently stop
// reconciling until the pod restarts.
func TestQuotaReconciler_KeepsTickingAfterErrors(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeQuotaStore{
		reconcile: func() (int64, error) { return 0, boom },
		roll:      func(time.Time) (int64, error) { return 0, boom },
	}
	q := &QuotaReconciler{Store: store, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- q.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if calls, _ := store.snapshot(); calls >= 2 {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("Run returned early (%v); want the loop to continue", err)
		case <-deadline:
			t.Fatal("did not observe repeated ticks after errors")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
