package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type countingReclaimRepo struct {
	calls chan time.Duration
}

func (r *countingReclaimRepo) ReclaimStale(_ context.Context, staleAfter time.Duration) (int64, error) {
	select {
	case r.calls <- staleAfter:
	default:
	}
	return 0, nil
}

// The job is wired to the housekeeping interval, which is an hour. Detection
// has to be bounded by StaleAfter instead: a caller is waiting on a stuck
// operation, and an hourly tick would stretch the worst case to 75 minutes.
func TestStaleOperationReclaimer_TickInterval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		interval   time.Duration
		staleAfter time.Duration
		want       time.Duration
	}{
		{"hourly caller, 15m stale", time.Hour, 15 * time.Minute, 5 * time.Minute},
		// The floor is absolute: nothing needs sub-minute reclaim when
		// staleness is measured in minutes, and this table has no index on
		// state alone.
		{"caller faster than the floor", 30 * time.Second, time.Hour, time.Minute},
		{"short stale is floored", time.Hour, time.Minute, time.Minute},
		{"floor applies to a third", time.Hour, 90 * time.Second, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &StaleOperationReclaimer{Interval: tc.interval, StaleAfter: tc.staleAfter}
			if got := w.tickInterval(); got != tc.want {
				t.Errorf("tickInterval() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A zero interval disables the job, matching every other housekeeping worker.
func TestStaleOperationReclaimer_DisabledByZeroInterval(t *testing.T) {
	repo := &countingReclaimRepo{calls: make(chan time.Duration, 1)}
	w := &StaleOperationReclaimer{Repo: repo, Interval: 0}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if len(repo.calls) != 0 {
		t.Error("disabled reclaimer still called the store")
	}
}

// The guard is a disjunction — a zero interval OR no repo disables the job —
// and the existing test above exercises only the interval half. Inverting the
// repo half makes a fully-configured reclaimer return immediately and never
// tick: abandoned operations then show in the console as running forever,
// which is the exact failure this worker exists to end.
//
// The observable is the return: a configured reclaimer enters RunTicker and
// comes back with the context's error, while a disabled one returns nil.
func TestStaleOperationReclaimer_ConfiguredRunnerActuallyRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := &StaleOperationReclaimer{
		Repo:     &countingReclaimRepo{calls: make(chan time.Duration, 1)},
		Interval: time.Minute,
	}
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled — a configured reclaimer that "+
			"returns nil never entered its loop, and nothing reclaims the "+
			"operations stuck at RUNNING", err)
	}
}
