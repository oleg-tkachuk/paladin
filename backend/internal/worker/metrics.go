package worker

import (
	"context"
	"sync"

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
