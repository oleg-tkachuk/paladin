package statemachine

import "github.com/prometheus/client_golang/prometheus"

// metricTransitionsTotal emits a label `source` ∈ {event, rpc, reconciler}
// and `target` ∈ {AVAILABLE, FAILED, DELETED}. Critical alert:
//
//	rate(paladin_object_transitions_total{source="reconciler",target="AVAILABLE"}[10m])
//	  / rate(paladin_object_transitions_total{target="AVAILABLE"}[10m]) > 0.05
//
// → event pipeline broken or latency ≫ reconciler TTL.
var metricTransitionsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "paladin",
		Subsystem: "object",
		Name:      "transitions_total",
		Help:      "Object state transitions by source signal.",
	},
	[]string{"source", "target"},
)

func init() {
	prometheus.MustRegister(metricTransitionsTotal)
}
