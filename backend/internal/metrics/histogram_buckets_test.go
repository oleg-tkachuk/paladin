package metrics

import (
	"context"
	"math"
	"testing"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// A duration histogram is only as good as its buckets. With the SDK default,
// sized for milliseconds, a seconds histogram puts every fast call in the
// first bucket (0–5s) and a p95 read off it is an interpolation in that
// bucket: live, it reported 4.75s for presigns that take a few milliseconds.
//
// So each histogram records one typical value and must land it in a bucket
// narrow enough to be read: an upper bound no more than a few times the value.
func TestDurationHistogramsResolveTypicalValues(t *testing.T) {
	cases := []struct {
		name    string
		build   func(metric.Meter) (metric.Float64Histogram, error)
		typical float64 // seconds
		// The bucket holding `typical` must close at or below this.
		maxUpperBound float64
	}{
		{"paladin_presign_duration_seconds", newPresignDuration, 0.003, 0.005},
		{"paladin_storage_call_duration_seconds", newStorageCallDuration, 0.02, 0.025},
		{"paladin_worker_run_duration_seconds", newWorkerRunDuration, 2, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
			h, err := tc.build(meter)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			h.Record(context.Background(), tc.typical)

			var rm metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &rm); err != nil {
				t.Fatalf("collect: %v", err)
			}
			dp := onlyHistogramPoint(t, rm, tc.name)
			upper := upperBoundHolding(dp)
			if upper > tc.maxUpperBound {
				t.Errorf("%gs landed in the bucket closing at %gs (bounds %v); want one closing at or below %gs",
					tc.typical, upper, dp.Bounds, tc.maxUpperBound)
			}
		})
	}
}

func onlyHistogramPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok || len(h.DataPoints) != 1 {
				t.Fatalf("%s: want one float64 histogram point, got %T", name, m.Data)
			}
			return h.DataPoints[0]
		}
	}
	t.Fatalf("%s was not collected", name)
	return metricdata.HistogramDataPoint[float64]{}
}

// upperBoundHolding returns the upper bound of the bucket that holds the
// point's single sample; the +Inf bucket is reported as math.MaxFloat64.
func upperBoundHolding(dp metricdata.HistogramDataPoint[float64]) float64 {
	for i, n := range dp.BucketCounts {
		if n == 0 {
			continue
		}
		if i < len(dp.Bounds) {
			return dp.Bounds[i]
		}
		break
	}
	return math.MaxFloat64
}
