package main

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// A kind's switch counts the enabled subscriptions of that kind only.
func TestSubscriptionCountIsPerKind(t *testing.T) {
	subs := sinkSubscriptions(func(_ context.Context, kind string) ([][]byte, error) {
		if kind == worker.SinkKindNATS {
			return make([][]byte, 2), nil
		}
		return nil, nil
	})
	for kind, want := range map[string]int{worker.SinkKindNATS: 2, worker.SinkKindRabbitMQ: 0} {
		if got, err := subs.count(kind)(context.Background()); err != nil || got != want {
			t.Errorf("count(%s) = %d, %v; want %d", kind, got, err, want)
		}
	}
}

func TestSubscriptionCountPassesTheReadErrorOn(t *testing.T) {
	denied := errors.New("permission denied")
	subs := sinkSubscriptions(func(context.Context, string) ([][]byte, error) { return nil, denied })
	if _, err := subs.count(worker.SinkKindNATS)(context.Background()); !errors.Is(err, denied) {
		t.Errorf("count err = %v, want %v", err, denied)
	}
}
