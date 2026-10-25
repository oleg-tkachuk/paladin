package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The names limes's instruments export a charge under: what Paladin's
// dashboards read now that Paladin counts no charge itself.
const (
	chargeDecisionsMetric = "limes.charge.decisions"
	tenantAttribute       = "limes.tenant_id"
)

// A charge through the usage store the app builds is counted by limes, with
// its tenant, on the global meter provider the app exports.
func TestMeteredUsageCountsAChargeByTenant(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	records, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: records})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := meteredUsage(store)
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	ctx := context.Background()
	c := limes.Capability{
		ID: uuid.New(), Subject: limes.Principal{TenantID: tenant}, ExpiresAt: time.Now().Add(time.Hour),
		Caveats: limes.Caveats{Budget: limes.BudgetCeiling{Unlimited: true}},
	}
	if err := records.Insert(ctx, limes.InsertRequest{Capability: c}); err != nil {
		t.Fatal(err)
	}
	subject, err := limes.StoredMeterSubject(ctx, records, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = usage.Charge(ctx, meter.ChargeRequest{Subject: subject, Amount: 1}, nil)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != chargeDecisionsMetric {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				if v, ok := dp.Attributes.Value(tenantAttribute); ok && v.AsString() == tenant.String() {
					return
				}
			}
		}
	}
	t.Errorf("no %s with %s=%s in %+v", chargeDecisionsMetric, tenantAttribute, tenant, rm.ScopeMetrics)
}
