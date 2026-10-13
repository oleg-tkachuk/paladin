package worker

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// TrashRetentionGauge publishes how many trashed tenants are past
// admin.tenants.trash_retention: the ones the console marks "Past retention"
// and nothing purges on its own. The console showed them on the Trash page
// alone, so an operator learned of a lapse only by opening it; this is what
// an alert can watch.
type TrashRetentionGauge struct {
	Store     TrashRetentionCounter
	Retention time.Duration
	Interval  time.Duration
	Logger    *zap.Logger
	// Now is the clock the lapse is judged by. Nil: time.Now.
	Now func() time.Time
}

// TrashRetentionCounter is the store seam: the trashed tenants whose
// retention lapsed at or before asOf.
type TrashRetentionCounter interface {
	CountTenantsPastTrashRetention(ctx context.Context, retention time.Duration, asOf time.Time) (int64, error)
}

// Run ticks until ctx is done. A zero Interval or Retention disables it.
func (g *TrashRetentionGauge) Run(ctx context.Context) error {
	if g.Interval <= 0 || g.Retention <= 0 || g.Store == nil {
		return nil
	}
	return RunTicker(ctx, "trash_retention_gauge", g.Interval, g.tick)
}

func (g *TrashRetentionGauge) tick(ctx context.Context) error {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	n, err := g.Store.CountTenantsPastTrashRetention(ctx, g.Retention, now())
	if err != nil {
		g.log().Warn("failed to count tenants past trash retention", zap.Error(err))
		return err
	}
	recordTenantsPastTrashRetention(ctx, n)
	return nil
}

func (g *TrashRetentionGauge) log() *zap.Logger {
	if g.Logger == nil {
		return zap.NewNop()
	}
	return g.Logger
}

var (
	trashRetentionMetricsOnce sync.Once
	tenantsPastTrashRetention metric.Int64Gauge
)

// recordTenantsPastTrashRetention publishes the count. No-op-safe before the
// MeterProvider is wired.
func recordTenantsPastTrashRetention(ctx context.Context, n int64) {
	trashRetentionMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/worker")
		tenantsPastTrashRetention, _ = meter.Int64Gauge(
			"paladin.tenants.past_trash_retention",
			metric.WithDescription("Trashed tenants past admin.tenants.trash_retention: surfaced for purge, which nothing does on its own."),
		)
	})
	if tenantsPastTrashRetention != nil {
		tenantsPastTrashRetention.Record(ctx, n)
	}
}
