package worker

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// One provider for the package, installed before any test can create the
// instruments — see the same note in internal/auth/api_token_metrics_test.go.
var workerMetricsReader = func() sdkmetric.Reader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

func gauge[N int64 | float64](t *testing.T, name string) (N, string, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := workerMetricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				points := m.Data.(metricdata.Gauge[N]).DataPoints
				return points[len(points)-1].Value, m.Unit, true
			}
		}
	}
	return 0, "", false
}

// What PaladinUploadsNotSettling reads: the overdue count, and the age in
// seconds — the unit the Prometheus name's _seconds suffix comes from.
func TestRecordPendingOverdue(t *testing.T) {
	recordPendingOverdue(context.Background(), 29, 90*time.Minute)

	if n, _, ok := gauge[int64](t, "paladin.objects.pending_overdue"); !ok || n != 29 {
		t.Errorf("pending_overdue = %d (found %v), want 29", n, ok)
	}
	age, unit, ok := gauge[float64](t, "paladin.objects.pending_overdue.age")
	if !ok || age != 5400 || unit != "s" {
		t.Errorf("pending_overdue.age = %v %q (found %v), want 5400 s", age, unit, ok)
	}

	// Settled: both go back to zero rather than keeping the last backlog.
	recordPendingOverdue(context.Background(), 0, 0)
	if age, _, _ := gauge[float64](t, "paladin.objects.pending_overdue.age"); age != 0 {
		t.Errorf("after settling, age = %v, want 0", age)
	}
}

// The backlog the dispatcher samples reaches both gauges.
func TestRecordOutboxDepth(t *testing.T) {
	const total, deepest = 42, 17
	recordOutboxDepth(context.Background(), total, deepest)
	if n, _, ok := gauge[int64](t, "paladin.outbox.pending"); !ok || n != total {
		t.Errorf("outbox.pending = %d (found %v), want %d", n, ok, total)
	}
	if n, _, ok := gauge[int64](t, "paladin.outbox.pending.max_per_tenant"); !ok || n != deepest {
		t.Errorf("outbox.pending.max_per_tenant = %d (found %v), want %d", n, ok, deepest)
	}
}
