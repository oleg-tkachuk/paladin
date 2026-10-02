package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// One provider for the package, installed before any test can create the
// instruments — see the same note in internal/auth/api_token_metrics_test.go.
var replicaMetricsReader = func() sdkmetric.Reader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

func collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := replicaMetricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func readsCount(t *testing.T, servedBy, reason string) int64 {
	t.Helper()
	m, ok := findMetric(collect(t), "paladin.db.replica.reads")
	if !ok {
		return 0
	}
	want := attribute.NewSet(attribute.String("served_by", servedBy), attribute.String("reason", reason))
	for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
		if dp.Attributes.Equals(&want) {
			return dp.Value
		}
	}
	return 0
}

// What an operator reads to tell whether the replica is doing anything: the
// in-sync gauge and lag follow the probe, and every read is counted where it
// ran and why.
func TestReplicaMetrics(t *testing.T) {
	lag := dur(500 * time.Millisecond)
	r, _, _ := testRouter(func() (*time.Duration, error) { return lag, nil }, time.Second)
	r.probe(context.Background())

	rm := collect(t)
	m, ok := findMetric(rm, "paladin.db.replica.in_sync")
	if !ok || m.Data.(metricdata.Gauge[int64]).DataPoints[0].Value != 1 {
		t.Fatalf("in_sync gauge = %+v, want 1", m.Data)
	}
	m, ok = findMetric(rm, "paladin.db.replica.lag")
	if !ok || m.Unit != "s" || m.Data.(metricdata.Gauge[float64]).DataPoints[0].Value != 0.5 {
		t.Fatalf("lag gauge = %+v (unit %q), want 0.5 s", m.Data, m.Unit)
	}

	before := readsCount(t, "replica", "in_sync")
	_ = r.Read(context.Background(), func(sqlc.DBTX) error { return nil })
	if got := readsCount(t, "replica", "in_sync"); got != before+1 {
		t.Errorf("replica reads %d → %d, want +1", before, got)
	}

	before = readsCount(t, "primary", "replica_error")
	_ = r.Read(context.Background(), func(db sqlc.DBTX) error {
		if db.(fakeDB).name == "replica" {
			return errors.New("conflict with recovery")
		}
		return nil
	})
	if got := readsCount(t, "primary", "replica_error"); got != before+1 {
		t.Errorf("fallback reads %d → %d, want +1", before, got)
	}

	lag = dur(5 * time.Second)
	r.probe(context.Background())
	m, _ = findMetric(collect(t), "paladin.db.replica.in_sync")
	if m.Data.(metricdata.Gauge[int64]).DataPoints[0].Value != 0 {
		t.Fatalf("in_sync gauge after falling behind = %+v, want 0", m.Data)
	}
	before = readsCount(t, "primary", "out_of_sync")
	_ = r.Read(context.Background(), func(sqlc.DBTX) error { return nil })
	if got := readsCount(t, "primary", "out_of_sync"); got != before+1 {
		t.Errorf("out-of-sync reads %d → %d, want +1", before, got)
	}
}

// A database with no replica counts nothing: the series would be noise on
// every deployment that never turned it on.
func TestPrimaryOnlyRouterCountsNothing(t *testing.T) {
	before := readsCount(t, "primary", "out_of_sync")
	_ = NewPrimaryOnlyRouter(fakeDB{"primary"}).Read(context.Background(), func(sqlc.DBTX) error { return nil })
	if got := readsCount(t, "primary", "out_of_sync"); got != before {
		t.Errorf("primary-only read was counted: %d → %d", before, got)
	}
}

func TestReplicaHealthErr(t *testing.T) {
	var lag *time.Duration
	var lagErr error
	r, _, _ := testRouter(func() (*time.Duration, error) { return lag, lagErr }, 2*time.Second)

	if err := r.HealthErr(); err == nil || !strings.Contains(err.Error(), "not probed") {
		t.Errorf("before the first probe: %v", err)
	}
	lag = dur(time.Second)
	r.probe(context.Background())
	if err := r.HealthErr(); err != nil {
		t.Errorf("in sync: %v", err)
	}
	lag = dur(3 * time.Second)
	r.probe(context.Background())
	if err := r.HealthErr(); err == nil || !strings.Contains(err.Error(), "3s behind (max_lag 2s)") {
		t.Errorf("behind: %v", err)
	}
	lag, lagErr = nil, errors.New("connection refused")
	r.probe(context.Background())
	if err := r.HealthErr(); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("unreachable: %v", err)
	}
	lagErr = nil
	r.probe(context.Background())
	if err := r.HealthErr(); err == nil || !strings.Contains(err.Error(), "lag unknown") {
		t.Errorf("unknown lag: %v", err)
	}
	if st := r.Status(); st.CheckedAt.IsZero() || st.InSync {
		t.Errorf("status = %+v", st)
	}
}
