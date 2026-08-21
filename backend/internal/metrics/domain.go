package metrics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Domain instruments for the operations Paladin exists to perform.
//
// otelconnect already gives RED for every RPC — rate, errors, duration by
// method. That answers "is the service up" but not "is it doing its job":
// a presign that consistently returns PermissionDenied is a healthy-looking
// RPC and a broken product, and a budget that rejects every charge shows up
// as ResourceExhausted with no way to tell an exhausted tenant from a
// misconfigured one.
//
// So these count the decisions, not the calls. Each is deliberately
// low-cardinality: outcomes are bounded enums and tenant_id is the finest
// dimension, for the reason the api_token instruments already document —
// per-object or per-capability labels blow the exporter.
var (
	meterDomain = otel.Meter("github.com/oleg-tkachuk/paladin/internal/metrics/domain")

	presignIssued  metric.Int64Counter
	presignSeconds metric.Float64Histogram

	capabilityCharges metric.Int64Counter
	capabilityAmount  metric.Float64Histogram

	objectLockOps metric.Int64Counter
)

func init() {
	var err error
	if presignIssued, err = meterDomain.Int64Counter(
		"paladin_presign_total",
		metric.WithDescription(
			"Presign requests by operation (get|put|part) and outcome. Paladin never "+
				"proxies bytes, so this is the closest thing to a throughput signal "+
				"the control plane has."),
	); err != nil {
		otel.Handle(err)
	}
	if presignSeconds, err = meterDomain.Float64Histogram(
		"paladin_presign_duration_seconds",
		metric.WithDescription("Presign wall-clock duration, including bucket resolution and the policy decision."),
		metric.WithUnit("s"),
	); err != nil {
		otel.Handle(err)
	}
	if capabilityCharges, err = meterDomain.Int64Counter(
		"paladin_capability_charges_total",
		metric.WithDescription(
			"Capability charge attempts by outcome (charged|capability_exhausted|"+
				"tenant_exhausted|error). A tenant whose charges are all rejected "+
				"cannot work, and looks identical to an idle one without this."),
	); err != nil {
		otel.Handle(err)
	}
	if capabilityAmount, err = meterDomain.Float64Histogram(
		"paladin_capability_charge_amount",
		metric.WithDescription("Amount per successful charge, in the capability's unit."),
		metric.WithUnit("1"),
	); err != nil {
		otel.Handle(err)
	}
	if objectLockOps, err = meterDomain.Int64Counter(
		"paladin_object_lock_operations_total",
		metric.WithDescription(
			"Object-lock writes by operation (retention|legal_hold), mode and outcome. "+
				"A COMPLIANCE retention cannot be shortened by anyone, so each one is "+
				"an irreversible commitment of storage — worth counting on its own."),
	); err != nil {
		otel.Handle(err)
	}
}

// RecordPresign counts one presign and its latency. op is get|put|part;
// outcome is ok, or the failure class the caller mapped it to.
func RecordPresign(ctx context.Context, op, outcome string, seconds float64) {
	if presignIssued == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("op", op),
		attribute.String("outcome", outcome),
	)
	presignIssued.Add(ctx, 1, attrs)
	if presignSeconds != nil {
		presignSeconds.Record(ctx, seconds, attrs)
	}
}

// RecordCapabilityCharge counts a charge attempt. Amount is recorded only for
// a successful one — a rejected charge has no amount, and folding zeros into
// the histogram would drag the distribution toward a number nobody spent.
func RecordCapabilityCharge(ctx context.Context, tenantID, outcome string, amount float64) {
	if capabilityCharges == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	capabilityCharges.Add(ctx, 1, metric.WithAttributes(attrs...))
	if outcome == "charged" && capabilityAmount != nil {
		capabilityAmount.Record(ctx, amount, metric.WithAttributes(attrs...))
	}
}

// RecordObjectLock counts an object-lock write. mode is GOVERNANCE,
// COMPLIANCE, or "" for a legal-hold toggle that names no mode.
func RecordObjectLock(ctx context.Context, op, mode, outcome string) {
	if objectLockOps == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("op", op),
		attribute.String("outcome", outcome),
	}
	if mode != "" {
		attrs = append(attrs, attribute.String("mode", mode))
	}
	objectLockOps.Add(ctx, 1, metric.WithAttributes(attrs...))
}
