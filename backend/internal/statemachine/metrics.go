package statemachine

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// transitionsTotal counts object state transitions by `source` ∈
// {event, rpc, reconciler} and `target` ∈ {AVAILABLE, FAILED, DELETED}.
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

func init() {
	var err error
	transitionsTotal, err = otel.Meter("github.com/oleg-tkachuk/paladin/statemachine").
		Int64Counter(
			"paladin_object_transitions",
			metric.WithDescription("Object state transitions by source signal."),
		)
	if err != nil {
		otel.Handle(err)
	}
}

// recordTransition records one object state transition.
func recordTransition(ctx context.Context, source, target string) {
	transitionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("source", source),
		attribute.String("target", target),
	))
}
