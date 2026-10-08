package capability_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/capability/memstore"
)

// countingProvider counts every measurement any of its instruments takes.
type countingProvider struct {
	noop.MeterProvider
	n *atomic.Int64
}

func (p countingProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return countingMeter{n: p.n}
}

type countingMeter struct {
	noop.Meter
	n *atomic.Int64
}

func (m countingMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return countingInt64{n: m.n}, nil
}

func (m countingMeter) Float64Counter(string, ...metric.Float64CounterOption) (metric.Float64Counter, error) {
	return countingFloat64{n: m.n}, nil
}

func (m countingMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return countingHistogram{n: m.n}, nil
}

type countingInt64 struct {
	noop.Int64Counter
	n *atomic.Int64
}

func (c countingInt64) Add(context.Context, int64, ...metric.AddOption) { c.n.Add(1) }

type countingFloat64 struct {
	noop.Float64Counter
	n *atomic.Int64
}

func (c countingFloat64) Add(context.Context, float64, ...metric.AddOption) { c.n.Add(1) }

type countingHistogram struct {
	noop.Float64Histogram
	n *atomic.Int64
}

func (c countingHistogram) Record(context.Context, float64, ...metric.RecordOption) { c.n.Add(1) }

// A replayed charge or settle moved nothing, so the decorator counts nothing:
// counting it again would report spend twice.
func TestMeteringStoreCountsNoReplay(t *testing.T) {
	var measurements atomic.Int64
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(countingProvider{n: &measurements})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	ctx := context.Background()
	usage := capability.WithMetering[struct{}](memstore.NewUsage[struct{}](nil))
	tenant, capID := uuid.New(), uuid.New()
	charge := capability.ChargeRequest{CapabilityID: capID, TenantID: tenant, Amount: 1, ExternalRef: "call-1"}

	if _, err := usage.Charge(ctx, charge, nil); err != nil {
		t.Fatal(err)
	}
	if measurements.Load() == 0 {
		t.Fatal("a charge took no measurement")
	}
	after := measurements.Load()
	if r, err := usage.Charge(ctx, charge, nil); err != nil || !r.Replayed {
		t.Fatalf("repeated charge = %+v, %v; want a replay", r, err)
	}
	if got := measurements.Load(); got != after {
		t.Errorf("a replayed charge took %d measurements", got-after)
	}

	res, err := usage.Reserve(ctx, capability.ReserveRequest{CapabilityID: capID, TenantID: tenant, Amount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usage.Settle(ctx, capability.SettleRequest{ReservationID: res.ID, Amount: 1}, nil); err != nil {
		t.Fatal(err)
	}
	after = measurements.Load()
	if r, err := usage.Settle(ctx, capability.SettleRequest{ReservationID: res.ID, Amount: 1}, nil); err != nil || !r.Replayed {
		t.Fatalf("repeated settle = %+v, %v; want a replay", r, err)
	}
	if got := measurements.Load(); got != after {
		t.Errorf("a replayed settle took %d measurements", got-after)
	}
}
