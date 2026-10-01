package metrics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	if presignSeconds, err = newPresignDuration(meterDomain); err != nil {
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
	if storageCalls, err = meterDomain.Int64Counter("paladin_storage_calls_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if storageSeconds, err = newStorageCallDuration(meterDomain); err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if quotaDecisions, err = meterDomain.Int64Counter("paladin_quota_decisions_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if loginAttempts, err = meterDomain.Int64Counter("paladin_login_attempts_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if idempotencyLookups, err = meterDomain.Int64Counter("paladin_idempotency_lookups_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}

	ctx := context.Background()
	RecordPresign(ctx, "put", "ok", 0.012)
	RecordCapabilityCharge(ctx, "t1", "charged", 2.5)
	RecordCapabilityCharge(ctx, "t1", "tenant_exhausted", 0)
	RecordObjectLock(ctx, "retention", "COMPLIANCE", "applied")
	RecordStorageCall(ctx, "primary", "HeadObject", "ok", 0.004)
	RecordQuotaDecision(ctx, "t1", "tenant", "allowed")
	RecordLoginAttempt(ctx, "invalid_credentials")
	RecordIdempotencyLookup(ctx, "/paladin.admin.v1.TenantService/CreateTenant", "replayed")

	got := names(collect(t, reader))
	for _, want := range []string{
		"paladin_presign_total",
		"paladin_presign_duration_seconds",
		"paladin_capability_charges_total",
		"paladin_capability_charge_amount",
		"paladin_object_lock_operations_total",
		"paladin_storage_calls_total",
		"paladin_storage_call_duration_seconds",
		"paladin_quota_decisions_total",
		"paladin_login_attempts_total",
		"paladin_idempotency_lookups_total",
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
	storageCalls, storageSeconds = nil, nil
	quotaDecisions = nil
	loginAttempts = nil
	idempotencyLookups = nil

	ctx := context.Background()
	RecordPresign(ctx, "get", "ok", 1)
	RecordCapabilityCharge(ctx, "t1", "charged", 1)
	RecordObjectLock(ctx, "legal_hold", "", "placed")
	RecordStorageCall(ctx, "primary", "HeadObject", "error", 1)
	RecordQuotaDecision(ctx, "t1", "bucket", "rejected")
	RecordLoginAttempt(ctx, "ok")
	RecordIdempotencyLookup(ctx, "m", "miss")
}

// attrsOf returns the attribute sets recorded for one metric.
func attrsOf(rm metricdata.ResourceMetrics, name string) []attribute.Set {
	var out []attribute.Set
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if s, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range s.DataPoints {
					out = append(out, dp.Attributes)
				}
			}
		}
	}
	return out
}

// A login metric that carried the subject would be unbounded AND a published
// record of who is being targeted — which is the data an attacker would most
// like to read off a metrics endpoint. The outcome is the whole signal.
func TestLoginMetricCarriesNothingButTheOutcome(t *testing.T) {
	reader := metric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	meterDomain = otel.Meter("test")
	var err error
	if loginAttempts, err = meterDomain.Int64Counter("paladin_login_attempts_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}

	RecordLoginAttempt(context.Background(), "invalid_credentials")

	sets := attrsOf(collect(t, reader), "paladin_login_attempts_total")
	if len(sets) != 1 {
		t.Fatalf("want 1 datapoint, got %d", len(sets))
	}
	if got := sets[0].Len(); got != 1 {
		t.Errorf("login carries %d attributes, want exactly 1 (outcome): %v", got, sets[0].Encoded(attribute.DefaultEncoder()))
	}
	if v, ok := sets[0].Value("outcome"); !ok || v.AsString() != "invalid_credentials" {
		t.Errorf("outcome = %v (present=%v), want invalid_credentials", v.AsString(), ok)
	}
}

// backend_id is what makes the storage metric answer "which store", and an
// empty one is omitted rather than recorded as "". A dashboard grouping by a
// label whose value is the empty string reads as a real backend named nothing.
func TestStorageCallOmitsAnEmptyBackendID(t *testing.T) {
	reader := metric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	meterDomain = otel.Meter("test")
	var err error
	if storageCalls, err = meterDomain.Int64Counter("paladin_storage_calls_total"); err != nil {
		t.Fatalf("counter: %v", err)
	}
	storageSeconds = nil

	RecordStorageCall(context.Background(), "", "HeadObject", "ok", 0.001)

	sets := attrsOf(collect(t, reader), "paladin_storage_calls_total")
	if len(sets) != 1 {
		t.Fatalf("want 1 datapoint, got %d", len(sets))
	}
	if _, ok := sets[0].Value("backend_id"); ok {
		t.Error("an empty backend_id was recorded as a label")
	}
}
