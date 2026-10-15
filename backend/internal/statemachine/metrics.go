package statemachine

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// transitionsTotal counts object state transitions by `source` ∈
// {event, rpc, reconciler} and `target` ∈ {AVAILABLE, DELETED}. A failed
// object is not a transition: it is discarded (failuresTotal).
//
// Emitted over OTLP; the collector renders it in Prometheus as
// paladin_object_transitions_total (counters get a _total suffix), the name the
// SLO alert below queries:
//
//	rate(paladin_object_transitions_total{source="reconciler",target="AVAILABLE"}[10m])
//	  / rate(paladin_object_transitions_total{target="AVAILABLE"}[10m]) > 0.05
//
// → event pipeline broken or latency ≫ reconciler TTL.
//
// (Was a prometheus.NewCounterVec on the default registry, which Paladin never
// served — so it collected no observable data. Migrated to the OTel meter
// 2026-06-29.)
var transitionsTotal metric.Int64Counter

// failuresTotal counts objects discarded because they could not become
// AVAILABLE, by `reason` (a FailureReason). The row is deleted on failure
// (DiscardFailed), so this and the log line are what remain of it; rendered as
// paladin_object_failures_total. A rise in presign_expired is uploads whose
// bytes never arrived — a client that cannot reach storage, or signs wrong.
var failuresTotal metric.Int64Counter

func init() {
	meter := otel.Meter("github.com/oleg-tkachuk/paladin/statemachine")
	var err error
	transitionsTotal, err = meter.Int64Counter(
		"paladin_object_transitions",
		metric.WithDescription("Object state transitions by source signal."),
	)
	if err != nil {
		otel.Handle(err)
	}
	failuresTotal, err = meter.Int64Counter(
		"paladin_object_failures",
		metric.WithDescription("Objects discarded because they could not become available, by reason."),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// recordFailure records one discarded object.
func recordFailure(ctx context.Context, reason FailureReason) {
	failuresTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", string(reason))))
}

// recordTransition records one object state transition.
func recordTransition(ctx context.Context, source, target string) {
	transitionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("source", source),
		attribute.String("target", target),
	))
}
