package capability

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// OpenTelemetry instruments for the capability runtime counters.
// Same lazy-init shape as internal/auth/api_token_metrics.go: every
// module that imports internal/capability doesn't pay for instrument
// creation when the OTel global MeterProvider isn't ready (tests,
// sidecar tools).
//
// Cardinality:
//
//   - tenant_id is the only label. Cap id is deliberately omitted —
//     a deployment with thousands of capabilities would blow the
//     OTLP exporter's series count. Sum-by-tenant is the operator's
//     usual filter anyway.
//   - "decision" is a bounded enum: allowed / cap_exceeded /
//     tenant_exceeded. "kind" likewise: request / charge / refund.
//
// Naming: `paladin.capability.<name>` to match the api_token convention.
var (
	capMetricsOnce sync.Once

	capChargeAmount    metric.Float64Counter   // total spent by tenant
	capChargeDecisions metric.Int64Counter     // allow/deny per attempt
	capRequestBumps    metric.Int64Counter     // allow/deny per attempt
	capRefundAmount    metric.Float64Counter   // total refunded by tenant
	capCurrentSpend    metric.Float64Histogram // distribution of post-charge spend
)

func initMetrics() {
	capMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/capability")

		capChargeAmount, _ = meter.Float64Counter(
			"paladin.capability.charge.amount_usd",
			metric.WithDescription("Cumulative USD charged against capability budgets, by tenant."),
			metric.WithUnit("USD"),
		)
		capChargeDecisions, _ = meter.Int64Counter(
			"paladin.capability.charge.decisions",
			metric.WithDescription("Charge attempts bucketed by outcome: allowed / cap_exceeded / tenant_exceeded."),
		)
		capRequestBumps, _ = meter.Int64Counter(
			"paladin.capability.request.bumps",
			metric.WithDescription("Per-capability request-count bumps, bucketed by outcome: allowed / limit_exceeded."),
		)
		capRefundAmount, _ = meter.Float64Counter(
			"paladin.capability.refund.amount_usd",
			metric.WithDescription("Cumulative USD refunded on capability counters."),
			metric.WithUnit("USD"),
		)
		capCurrentSpend, _ = meter.Float64Histogram(
			"paladin.capability.charge.current_spend_usd",
			metric.WithDescription("Per-capability spend after each successful charge — distribution by tenant."),
			metric.WithUnit("USD"),
		)
	})
}

// recordChargeAttempt is called once per Charge regardless of outcome.
// outcome is one of:
//
//	"allowed"          — both caps satisfied
//	"cap_exceeded"     — per-capability cap rejected
//	"tenant_exceeded"  — tenant aggregate cap rejected after the
//	                     capability cap accepted (the inner refund is
//	                     reflected in capRefundAmount separately)
func recordChargeAttempt(ctx context.Context, tenantID uuid.UUID, amountUSD, postSpend float64, outcome string) {
	initMetrics()
	if capChargeDecisions == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("outcome", outcome),
	}
	if tenantID != uuid.Nil {
		attrs = append(attrs, attribute.String("tenant_id", tenantID.String()))
	}
	capChargeDecisions.Add(ctx, 1, metric.WithAttributes(attrs...))
	if outcome == "allowed" && capChargeAmount != nil {
		capChargeAmount.Add(ctx, amountUSD, metric.WithAttributes(attrs...))
		if capCurrentSpend != nil {
			capCurrentSpend.Record(ctx, postSpend, metric.WithAttributes(attrs...))
		}
	}
}

// recordRequestBump is called once per BumpRequest regardless of outcome.
// outcome ∈ {"allowed", "limit_exceeded"}.
func recordRequestBump(ctx context.Context, tenantID uuid.UUID, outcome string) {
	initMetrics()
	if capRequestBumps == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("outcome", outcome),
	}
	if tenantID != uuid.Nil {
		attrs = append(attrs, attribute.String("tenant_id", tenantID.String()))
	}
	capRequestBumps.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// recordRefund is called for both per-capability and per-tenant refunds.
// scope ∈ {"capability", "tenant"}.
func recordRefund(ctx context.Context, tenantID uuid.UUID, amountUSD float64, scope string) {
	initMetrics()
	if capRefundAmount == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("scope", scope),
	}
	if tenantID != uuid.Nil {
		attrs = append(attrs, attribute.String("tenant_id", tenantID.String()))
	}
	capRefundAmount.Add(ctx, amountUSD, metric.WithAttributes(attrs...))
}
