package worker

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// The groups the dispatcher's details fall in on the health page.
const (
	detailGroupQueue  = "queue"
	detailGroupOldest = "oldest pending"
)

// OutboxComponent is the dispatcher's outbox: reachable, and how many events
// wait to be fanned out to their subscriptions. A backlog is not a failure —
// operators route on the count metric — so only an unreadable table is.
func OutboxComponent(pending func(context.Context) (int64, error)) health.Check {
	var (
		mu   sync.Mutex
		last *int64
	)
	return health.Check{
		Name:     "outbox",
		Category: health.CategorySubsystem,
		Critical: true,
		Func: func(ctx context.Context) error {
			n, err := pending(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				last = nil
				return err
			}
			last = &n
			return nil
		},
		Describe: func(context.Context) []health.Detail {
			mu.Lock()
			defer mu.Unlock()
			if last == nil {
				return nil
			}
			return []health.Detail{{Name: "events waiting", Value: strconv.FormatInt(*last, 10)}}
		},
	}
}

// DeliveriesComponent is the dispatcher's deliveries to subscriptions: how
// many wait and how many failed, how long the oldest has waited, and how many
// subscriptions have work stuck. Like the outbox, a backlog is not a failure;
// the console's dispatcher view breaks it down by subscription. Never
// critical: deliveries late are not a pod out of service.
func DeliveriesComponent(stats func(context.Context) (*DeliveryStats, error)) health.Check {
	var (
		mu   sync.Mutex
		last *DeliveryStats
	)
	return health.Check{
		Name:     "deliveries",
		Category: health.CategorySubsystem,
		Func: func(ctx context.Context) error {
			s, err := stats(ctx)
			mu.Lock()
			defer mu.Unlock()
			last = s
			return err
		},
		Describe: func(context.Context) []health.Detail {
			mu.Lock()
			defer mu.Unlock()
			if last == nil {
				return nil
			}
			oldest := "none"
			if last.OldestPendingSeconds > 0 {
				oldest = (time.Duration(last.OldestPendingSeconds) * time.Second).String()
			}
			return []health.Detail{
				{Group: detailGroupQueue, Name: "pending", Value: strconv.FormatInt(last.Pending, 10)},
				{Group: detailGroupQueue, Name: "failed", Value: strconv.FormatInt(last.Failed, 10)},
				{Group: detailGroupQueue, Name: "subscriptions stuck", Value: strconv.Itoa(len(last.Subscriptions))},
				{Name: detailGroupOldest, Value: oldest},
			}
		},
	}
}
