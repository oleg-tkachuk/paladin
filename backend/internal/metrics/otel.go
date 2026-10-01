package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("github.com/oleg-tkachuk/paladin")

	// resourceNameShapeTotal feeds the Phase-3 decision on deprecating a
	// redundant object-name shape (see internal/api/connectshim/resolve).
	// Flows over the OTLP pipeline like every other Paladin metric.
	resourceNameShapeTotal metric.Int64Counter
)

func init() {
	var err error
	resourceNameShapeTotal, err = meter.Int64Counter(
		"paladin_resource_name_shape_total",
		metric.WithDescription("Collection resource-name shapes received at the connectshim edge, by shape"),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// RecordResourceNameShape counts which object-name shape (canonical / tenant /
// bare) a request used at the connectshim edge.
func RecordResourceNameShape(ctx context.Context, shape string) {
	resourceNameShapeTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("shape", shape)))
}

// ─── Background-worker tick instrumentation ────────────────────────────────
// Every periodic worker (reconciler, purgers, reapers, lifecycle, replication)
// records one tick outcome + duration here, plus its last-run timestamp and
// configured interval as gauges. The "stalled" alert is then expressible
// without per-worker thresholds:
//
//	time() - paladin_worker_last_run_timestamp_seconds > 5 * paladin_worker_interval_seconds
//
// All flow over the same OTLP pipeline as every other Paladin metric (otel: false
// locally → no-op).
// newWorkerRunDuration builds the tick-duration histogram; see
// newPresignDuration for why it is a constructor.
func newWorkerRunDuration(m metric.Meter) (metric.Float64Histogram, error) {
	return m.Float64Histogram(
		"paladin_worker_run_duration_seconds",
		metric.WithDescription("Background-worker tick wall-clock duration, by worker"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(workerRunBucketsSeconds...),
	)
}

var (
	workerRunsTotal     metric.Int64Counter
	workerRunDuration   metric.Float64Histogram
	workerLastRunGauge  metric.Float64Gauge
	workerIntervalGauge metric.Float64Gauge
)

func init() {
	var err error
	if workerRunsTotal, err = meter.Int64Counter(
		"paladin_worker_runs_total",
		metric.WithDescription("Background-worker ticks, by worker and outcome (success|error)"),
	); err != nil {
		otel.Handle(err)
	}
	if workerRunDuration, err = newWorkerRunDuration(meter); err != nil {
		otel.Handle(err)
	}
	if workerLastRunGauge, err = meter.Float64Gauge(
		"paladin_worker_last_run_timestamp_seconds",
		metric.WithDescription("Unix timestamp of the last completed tick, by worker (stall-alert numerator)"),
		metric.WithUnit("s"),
	); err != nil {
		otel.Handle(err)
	}
	if workerIntervalGauge, err = meter.Float64Gauge(
		"paladin_worker_interval_seconds",
		metric.WithDescription("Configured tick interval, by worker (stall-alert denominator)"),
		metric.WithUnit("s"),
	); err != nil {
		otel.Handle(err)
	}
}

// RecordWorkerTick records one worker tick: increments the run counter with a
// success|error outcome, observes the duration, and stamps the last-run gauge.
func RecordWorkerTick(ctx context.Context, worker string, err error, dur time.Duration) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	workerRunsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("worker", worker),
		attribute.String("outcome", outcome),
	))
	w := metric.WithAttributes(attribute.String("worker", worker))
	workerRunDuration.Record(ctx, dur.Seconds(), w)
	workerLastRunGauge.Record(ctx, float64(time.Now().Unix()), w)
}

// SetWorkerInterval publishes a worker's configured tick interval so stall
// alerts can compare elapsed-since-last-run against N× the interval.
func SetWorkerInterval(ctx context.Context, worker string, interval time.Duration) {
	workerIntervalGauge.Record(ctx, interval.Seconds(), metric.WithAttributes(
		attribute.String("worker", worker),
	))
}
