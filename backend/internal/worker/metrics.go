package worker

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Outbox backlog gauges. Same lazy-init shape as internal/capability/metrics.go
// so importers don't pay for instrument creation before the OTel MeterProvider
// is ready.
//
// Cardinality: NO tenant label. The whole point is a cheap, bounded signal —
// the cluster-wide pending count plus the single deepest per-tenant backlog.
// That is the fan-out-volume measurement operators need to decide whether the
// drain (dispatcher.batch_size / poll_interval) keeps up, or a tenant's
// subscription filters are too broad. A per-tenant gauge would reintroduce the
// unbounded-series problem the backlog concern is about.
var (
	outboxMetricsOnce sync.Once

	outboxPendingTotal metric.Int64Gauge // event_deliveries in status=pending, cluster-wide
	outboxPendingMax   metric.Int64Gauge // deepest single-tenant pending backlog
)

func initOutboxMetrics() {
	outboxMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/worker")

		outboxPendingTotal, _ = meter.Int64Gauge(
			"paladin.outbox.pending",
			metric.WithDescription("event_deliveries rows in status=pending, cluster-wide (drain backlog)."),
		)
		outboxPendingMax, _ = meter.Int64Gauge(
			"paladin.outbox.pending.max_per_tenant",
			metric.WithDescription("Deepest per-tenant pending backlog — the fan-out-volume signal for drain / admission-control tuning."),
		)
	})
}

// recordOutboxDepth publishes the sampled backlog gauges. No-op-safe before the
// MeterProvider is wired (the instruments are nil until initOutboxMetrics).
func recordOutboxDepth(ctx context.Context, total, maxPerTenant int64) {
	initOutboxMetrics()
	if outboxPendingTotal != nil {
		outboxPendingTotal.Record(ctx, total)
	}
	if outboxPendingMax != nil {
		outboxPendingMax.Record(ctx, maxPerTenant)
	}
}

// Overdue-upload gauges, sampled by the reconciler after each tick: the
// PENDING objects it should already have settled, and how far past that
// deadline the oldest is. reconcile() logs a failed HEAD or promote and moves
// on, so the tick still succeeds and PaladinWorkerTicksAllFailing never
// fires; these are what show uploads the reconciler cannot settle. No tenant
// label, for the outbox gauges' reason.
var (
	pendingMetricsOnce sync.Once

	pendingOverdueCount metric.Int64Gauge   // PENDING objects past presign expiry + grace
	pendingOverdueAge   metric.Float64Gauge // how far past that the oldest is
)

func initPendingMetrics() {
	pendingMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/worker")

		pendingOverdueCount, _ = meter.Int64Gauge(
			"paladin.objects.pending_overdue",
			metric.WithDescription("PENDING objects past their presign expiry plus the reconciler's grace: uploads it should already have settled."),
		)
		pendingOverdueAge, _ = meter.Float64Gauge(
			"paladin.objects.pending_overdue.age",
			metric.WithDescription("How far past the reconciler's deadline the oldest overdue PENDING object is; 0 with none."),
			metric.WithUnit("s"),
		)
	})
}

// recordPendingOverdue publishes the sampled overdue-upload gauges. No-op-safe
// before the MeterProvider is wired.
func recordPendingOverdue(ctx context.Context, count int64, oldest time.Duration) {
	initPendingMetrics()
	if pendingOverdueCount != nil {
		pendingOverdueCount.Record(ctx, count)
	}
	if pendingOverdueAge != nil {
		pendingOverdueAge.Record(ctx, oldest.Seconds())
	}
}
