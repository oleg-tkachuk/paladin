package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// eventDeliveryPurgeBatch bounds one DELETE, so a backlog is drained in
// short transactions rather than one that locks it all.
const eventDeliveryPurgeBatch = 10000

// defaultEventDeliveryPurgeInterval paces the purge when no interval is set.
const defaultEventDeliveryPurgeInterval = time.Hour

// EventDeliveryPurger deletes outbox rows the dispatcher is done with —
// delivered, or failed after their last attempt — once that attempt is older
// than TTL. Pending rows are never touched. Disabled when TTL == 0.
//
// A failed row can be sent again only until it is purged, so TTL is also how
// long an operator has to redrive it.
type EventDeliveryPurger struct {
	Repo     EventDeliveryPurgerRepo
	TTL      time.Duration
	Interval time.Duration
	Logger   *zap.Logger
}

// EventDeliveryPurgerRepo is the narrow seam over the outbox table.
type EventDeliveryPurgerRepo interface {
	PurgeTerminalBefore(ctx context.Context, cutoff time.Time, batchSize int32) (int64, error)
}

func (p *EventDeliveryPurger) Run(ctx context.Context) error {
	if p.TTL <= 0 {
		return nil
	}
	if p.Interval <= 0 {
		p.Interval = defaultEventDeliveryPurgeInterval
	}
	return RunTicker(ctx, "event_delivery_purger", p.Interval, func(ctx context.Context) error {
		return p.drain(ctx, time.Now().UTC().Add(-p.TTL))
	})
}

// drain repeats the bounded purge until a batch comes back short.
func (p *EventDeliveryPurger) drain(ctx context.Context, cutoff time.Time) error {
	for {
		n, err := p.Repo.PurgeTerminalBefore(ctx, cutoff, eventDeliveryPurgeBatch)
		if err != nil {
			p.log().Warn("failed to purge event deliveries", zap.Error(err))
			return err
		}
		if n > 0 {
			p.log().Info("purged event deliveries", zap.Int64("rows", n), zap.Time("older_than", cutoff))
		}
		if n < eventDeliveryPurgeBatch {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (p *EventDeliveryPurger) log() *zap.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return zap.NewNop()
}
