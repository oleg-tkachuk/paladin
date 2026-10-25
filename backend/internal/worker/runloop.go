package worker

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// RunTicker drives a periodic background worker. It publishes the worker's
// configured interval, then on every tick runs fn and records the outcome
// (success|error), duration, and last-run timestamp via the metrics package.
// It blocks until ctx is cancelled and returns ctx.Err() then.
//
// Workers that previously hand-rolled `time.NewTicker` + for/select adopt this
// so the whole worker fleet is uniformly observable and the "stalled worker"
// alert (the chart's templates/_alerts-workers.tpl) has data to fire on.
//
// fn returning an error marks the tick as failed for metrics only — it does
// NOT stop the loop. Workers keep their own log-and-continue behaviour; they
// just return the tick's representative error so the outcome label is honest.
// A non-positive interval disables the worker (returns nil immediately), which
// matches the pre-existing `if interval <= 0 { return nil }` guards.
func RunTicker(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) error {
	if interval <= 0 {
		return nil
	}
	metrics.SetWorkerInterval(ctx, name, interval)
	Ticks.register(name, interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			start := time.Now()
			err := fn(ctx)
			took := time.Since(start)
			metrics.RecordWorkerTick(ctx, name, err, took)
			Ticks.record(name, start, took, err)
		}
	}
}

// keepFirst stores err in *first unless an earlier error is already there.
// Ticks that log a failure and carry on through the rest of their batch use it
// to return the first failure they met, which is what RunTicker records.
func keepFirst(first *error, err error) {
	if *first == nil {
		*first = err
	}
}
