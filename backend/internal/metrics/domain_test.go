package metrics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The instruments are package-level and created in init() against whatever
// MeterProvider is installed at import time — which in production is the OTel
// global, swapped in later by InitOTel. That ordering is easy to get wrong in
// a way nothing catches: a nil instrument silently records nothing, and the
// guard clauses in this package make that failure completely quiet.
//
// So this collects real data through an SDK reader rather than asserting the
// calls do not panic. A metric that compiles and never reaches a collector is
// the thing worth failing on, and it is exactly what the dashboards would
// discover instead.

func collect(t *testing.T, reader metric.Reader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func names(rm metricdata.ResourceMetrics) map[string]bool {
	out := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = true
		}
	}
	return out
}

func TestDomainInstrumentsReachACollector(t *testing.T) {
	reader := metric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	// Re-create the instruments against the reader-backed provider: the
	// package-level ones were bound at init() to whatever was global then.
	meterDomain = otel.Meter("test")
	var err error
	if presignIssued, err = meterDomain.Int64Counter("paladin_presign_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if presignSeconds, err = meterDomain.Float64Histogram("paladin_presign_duration_seconds"); err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if capabilityCharges, err = meterDomain.Int64Counter("paladin_capability_charges_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if capabilityAmount, err = meterDomain.Float64Histogram("paladin_capability_charge_amount"); err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if objectLockOps, err = meterDomain.Int64Counter("paladin_object_lock_operations_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}

	ctx := context.Background()
	RecordPresign(ctx, "put", "ok", 0.012)
	RecordCapabilityCharge(ctx, "t1", "charged", 2.5)
	RecordCapabilityCharge(ctx, "t1", "tenant_exhausted", 0)
	RecordObjectLock(ctx, "retention", "COMPLIANCE", "applied")

	got := names(collect(t, reader))
	for _, want := range []string{
		"paladin_presign_total",
		"paladin_presign_duration_seconds",
		"paladin_capability_charges_total",
		"paladin_capability_charge_amount",
		"paladin_object_lock_operations_total",
	} {
		if !got[want] {
			t.Errorf("%s never reached the collector", want)
		}
	}
}

// A rejected charge has no amount. Folding a zero into the histogram would
// drag the spend distribution toward a number nobody spent — an operator
// reading a p50 would see the rejection rate, not the typical charge.
func TestRejectedChargeRecordsNoAmount(t *testing.T) {
	reader := metric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	meterDomain = otel.Meter("test")
	var err error
	if capabilityCharges, err = meterDomain.Int64Counter("paladin_capability_charges_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if capabilityAmount, err = meterDomain.Float64Histogram("paladin_capability_charge_amount"); err != nil {
		t.Fatalf("histogram: %v", err)
	}

	RecordCapabilityCharge(context.Background(), "t1", "capability_exhausted", 0)

	for _, sm := range collect(t, reader).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "paladin_capability_charge_amount" {
				continue
			}
			if h, ok := m.Data.(metricdata.Histogram[float64]); ok && len(h.DataPoints) > 0 {
				t.Errorf("a rejected charge recorded an amount: %d datapoints", len(h.DataPoints))
			}
		}
	}
}

// The guard clauses return early on a nil instrument, which is what keeps a
// test or a tool that never called InitOTel from panicking. Worth pinning:
// without it, importing this package from anywhere without a MeterProvider
// would crash on the first record.
func TestRecordingWithNilInstrumentsIsSafe(t *testing.T) {
	presignIssued, presignSeconds = nil, nil
	capabilityCharges, capabilityAmount = nil, nil
	objectLockOps = nil

	ctx := context.Background()
	RecordPresign(ctx, "get", "ok", 1)
	RecordCapabilityCharge(ctx, "t1", "charged", 1)
	RecordObjectLock(ctx, "legal_hold", "", "placed")
}
