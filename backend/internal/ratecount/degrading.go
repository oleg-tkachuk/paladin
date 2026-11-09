package ratecount

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Degrading counts on a shared store and, for each call the store cannot
// answer, on this process instead (ADR-0046, decision 3). A limit then stays
// in force at the configured rate per replica rather than being lifted.
//
// The switch either way is logged once, not per call: an outage would
// otherwise log every request. Every call decided locally is counted on
// paladin.ratelimit.degraded, so the duration and reach of an outage show.
type Degrading struct {
	shared   Counter
	local    *Local
	backend  string
	log      *zap.Logger
	degraded atomic.Bool
	calls    metric.Int64Counter
}

// degradedMetric names the counter of calls decided in this process.
const degradedMetric = "paladin.ratelimit.degraded"

// NewDegrading wraps shared. backend names the store in logs and metrics.
func NewDegrading(shared Counter, backend string, log *zap.Logger) *Degrading {
	calls, err := otel.Meter("github.com/oleg-tkachuk/paladin/internal/ratecount").Int64Counter(
		degradedMetric,
		metric.WithDescription(
			"Rate-limit calls decided in this process because the shared store did not "+
				"answer. Non-zero means each replica grants the full rate."),
	)
	if err != nil {
		log.Warn("rate limit degraded counter not registered", zap.Error(err))
	}
	return &Degrading{shared: shared, local: NewLocal(), backend: backend, log: log, calls: calls}
}

// settle records how the shared store answered, logging a change of state.
func (d *Degrading) settle(ctx context.Context, err error) {
	if err == nil {
		if d.degraded.CompareAndSwap(true, false) {
			d.log.Info("rate limits counting on the shared store again",
				zap.String("backend", d.backend))
		}
		return
	}
	if d.calls != nil {
		d.calls.Add(ctx, 1, metric.WithAttributes(attribute.String("backend", d.backend)))
	}
	if d.degraded.CompareAndSwap(false, true) {
		d.log.Warn("rate limits counting per process: the shared store did not answer; "+
			"each replica now grants the full rate",
			zap.String("backend", d.backend), zap.Error(err))
	}
}

// Bump implements Counter.
func (d *Degrading) Bump(ctx context.Context, key string) (Counts, error) {
	c, err := d.shared.Bump(ctx, key)
	d.settle(ctx, err)
	if err != nil {
		return d.local.Bump(ctx, key)
	}
	return c, nil
}

// BumpIfFits implements Counter.
func (d *Degrading) BumpIfFits(ctx context.Context, key string, capacity int) (Counts, error) {
	c, err := d.shared.BumpIfFits(ctx, key, capacity)
	d.settle(ctx, err)
	if err != nil {
		return d.local.BumpIfFits(ctx, key, capacity)
	}
	return c, nil
}

// Read implements Counter. A read the shared store cannot answer shows what
// this process counted, which is what decided the calls meanwhile.
func (d *Degrading) Read(ctx context.Context, key string) (Counts, error) {
	c, err := d.shared.Read(ctx, key)
	d.settle(ctx, err)
	if err != nil {
		return d.local.Read(ctx, key)
	}
	return c, nil
}
