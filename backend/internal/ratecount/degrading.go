package ratecount

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Fallback tracks whether a shared store is answering. Each change between
// shared and per-process counting is logged once, not per call: an outage
// would otherwise log every request. Every call decided in the process
// meanwhile is counted on paladin.ratelimit.degraded, so the duration and
// reach of an outage show (ADR-0046, decision 3).
type Fallback struct {
	backend  string
	log      *zap.Logger
	degraded atomic.Bool
	calls    metric.Int64Counter
}

// degradedMetric names the counter of calls decided in this process.
const degradedMetric = "paladin.ratelimit.degraded"

// NewFallback tracks the store backend names in logs and metrics.
func NewFallback(backend string, log *zap.Logger) *Fallback {
	calls, err := otel.Meter("github.com/oleg-tkachuk/paladin/internal/ratecount").Int64Counter(
		degradedMetric,
		metric.WithDescription(
			"Rate-limit calls decided in this process because the shared store did not "+
				"answer. Non-zero means each replica grants the full rate."),
	)
	if err != nil {
		log.Warn("rate limit degraded counter not registered", zap.Error(err))
	}
	return &Fallback{backend: backend, log: log, calls: calls}
}

// Settle records how the shared store last answered, logging a change.
func (f *Fallback) Settle(err error) {
	if err == nil {
		if f.degraded.CompareAndSwap(true, false) {
			f.log.Info("rate limits counting on the shared store again",
				zap.String("backend", f.backend))
		}
		return
	}
	if f.degraded.CompareAndSwap(false, true) {
		f.log.Warn("rate limits counting per process: the shared store did not answer; "+
			"each replica now grants the full rate",
			zap.String("backend", f.backend), zap.Error(err))
	}
}

// Degraded reports whether the last answer was a failure.
func (f *Fallback) Degraded() bool { return f.degraded.Load() }

// CountLocal records one call decided in the process.
func (f *Fallback) CountLocal(ctx context.Context) {
	if f.calls != nil {
		f.calls.Add(ctx, 1, metric.WithAttributes(attribute.String("backend", f.backend)))
	}
}

// Degrading counts on a shared store and, for each call the store cannot
// answer, on this process instead, so a limit stays in force at the
// configured rate per replica rather than being lifted.
type Degrading struct {
	shared   Counter
	local    *Local
	fallback *Fallback
}

// NewDegrading wraps shared. backend names the store in logs and metrics.
func NewDegrading(shared Counter, backend string, log *zap.Logger) *Degrading {
	return &Degrading{shared: shared, local: NewLocal(), fallback: NewFallback(backend, log)}
}

// answered settles the store's answer and reports whether to fall back.
func (d *Degrading) answered(ctx context.Context, err error) bool {
	d.fallback.Settle(err)
	if err != nil {
		d.fallback.CountLocal(ctx)
		return false
	}
	return true
}

// Bump implements Counter.
func (d *Degrading) Bump(ctx context.Context, key string) (Counts, error) {
	if c, err := d.shared.Bump(ctx, key); d.answered(ctx, err) {
		return c, nil
	}
	return d.local.Bump(ctx, key)
}

// BumpIfFits implements Counter.
func (d *Degrading) BumpIfFits(ctx context.Context, key string, capacity int) (Counts, error) {
	if c, err := d.shared.BumpIfFits(ctx, key, capacity); d.answered(ctx, err) {
		return c, nil
	}
	return d.local.BumpIfFits(ctx, key, capacity)
}

// Read implements Counter. A read the shared store cannot answer shows what
// this process counted, which is what decided the calls meanwhile.
func (d *Degrading) Read(ctx context.Context, key string) (Counts, error) {
	if c, err := d.shared.Read(ctx, key); d.answered(ctx, err) {
		return c, nil
	}
	return d.local.Read(ctx, key)
}
