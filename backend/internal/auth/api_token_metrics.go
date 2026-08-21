package auth

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// OpenTelemetry instruments for the api_token verify + rate-limit
// path. Lazy-initialised under sync.Once so every module that imports
// internal/auth doesn't pay for instrument creation when the package
// global MeterProvider isn't ready yet (test paths, sidecar tools).
//
// Cardinality discipline: tenant_id is the granularity. Token id is
// deliberately omitted — high-cardinality dims blow OTLP exporters.
// "allowed" / "code" are bounded enums; safe.
//
// Metric naming follows the Paladin convention: `paladin.<subsystem>.<name>`.
// Counters end with their unit; histograms note the unit on the
// instrument options.
var (
	apiTokMetricsOnce sync.Once

	apiTokVerifyDuration metric.Float64Histogram
	apiTokRLDecisions    metric.Int64Counter
	apiTokRLWeighted     metric.Float64Histogram
	apiTokRLFailOpen     metric.Int64Counter
)

// initAPITokenMetrics wires the instruments against otel's package
// global MeterProvider. Called the first time an interceptor needs to
// emit. Re-entrant — sync.Once gates initialisation, subsequent calls
// are no-ops.
func initAPITokenMetrics() {
	apiTokMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/auth")

		apiTokVerifyDuration, _ = meter.Float64Histogram(
			"paladin.api_token.verify.duration_ms",
			metric.WithDescription("End-to-end api_token verify latency including argon2id, in milliseconds."),
			metric.WithUnit("ms"),
		)
		apiTokRLDecisions, _ = meter.Int64Counter(
			"paladin.api_token.ratelimit.decisions",
			metric.WithDescription("Per-token sliding-window rate-limit decisions emitted at the verify path."),
		)
		apiTokRLWeighted, _ = meter.Float64Histogram(
			"paladin.api_token.ratelimit.weighted_ratio",
			metric.WithDescription("Weighted-window count divided by token capacity. >1.0 means denied."),
			metric.WithUnit("1"),
		)
		apiTokRLFailOpen, _ = meter.Int64Counter(
			"paladin.api_token.ratelimit.fail_open",
			metric.WithDescription(
				"Requests admitted without a rate-limit decision because the limiter errored. "+
					"Non-zero means rate limiting is not in force."),
		)
	})
}

// recordVerifyDuration writes a sample to the verify-latency histogram.
// Cheap when the meter is a no-op (the OTel default before InitOTel
// runs); we still gate on the instrument being non-nil so testing
// without a MeterProvider doesn't panic.
func recordVerifyDuration(ctx context.Context, ms float64, tenantID string, ok bool) {
	initAPITokenMetrics()
	if apiTokVerifyDuration == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.Bool("ok", ok),
	}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	apiTokVerifyDuration.Record(ctx, ms, metric.WithAttributes(attrs...))
}

// recordRateLimitDecision bumps the decisions counter and the
// weighted-ratio histogram. Called from the interceptor's rateLimitGate
// regardless of allow/deny.
func recordRateLimitDecision(ctx context.Context, tenantID string, allowed bool, weighted float64, capacity int) {
	initAPITokenMetrics()
	if apiTokRLDecisions == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.Bool("allowed", allowed),
	}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	apiTokRLDecisions.Add(ctx, 1, metric.WithAttributes(attrs...))

	if capacity > 0 {
		apiTokRLWeighted.Record(ctx, weighted/float64(capacity), metric.WithAttributes(attrs...))
	}
}

// recordRateLimitFailOpen counts a request admitted WITHOUT a rate-limit
// decision because the limiter itself failed.
//
// The interceptor fails open on purpose — a limiter outage should not deny
// requests whose token already passed signature, expiry and audience. But
// failing open silently means rate limiting can stop existing and nothing
// says so: the decisions counter simply goes quiet, which looks identical to
// no traffic. This is the signal that separates the two, and it belongs in an
// alert — a non-zero rate here means every token is effectively uncapped.
func recordRateLimitFailOpen(ctx context.Context, tenantID string) {
	initAPITokenMetrics()
	if apiTokRLFailOpen == nil {
		return
	}
	attrs := []attribute.KeyValue{}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	apiTokRLFailOpen.Add(ctx, 1, metric.WithAttributes(attrs...))
}
