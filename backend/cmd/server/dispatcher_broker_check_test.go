package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// subsOf answers with n sink configs for kind and none for any other kind.
func subsOf(kind string, n int) sinkSubscriptions {
	return func(_ context.Context, k string) ([][]byte, error) {
		if k != kind {
			return nil, nil
		}
		return make([][]byte, n), nil
	}
}

func noConns() []worker.BrokerConn { return nil }

// The rabbitmq row read healthy on a dispatcher no subscription used it
// from: an empty pool had nothing failing. It asks the subscriptions now.
func TestBrokerCheckIsNotInUseWithoutASubscription(t *testing.T) {
	check := brokerCheck(worker.SinkKindRabbitMQ, subsOf(worker.SinkKindNATS, 1), noConns)
	var unused *health.NotInUseError
	if err := check(context.Background()); !errors.As(err, &unused) {
		t.Fatalf("check = %v, want not in use", err)
	}
}

func TestBrokerCheckCountsTheKindsSubscriptions(t *testing.T) {
	check := brokerCheck(worker.SinkKindRabbitMQ, subsOf(worker.SinkKindRabbitMQ, 3), noConns)
	err := check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "3 enabled subscription(s)") {
		t.Fatalf("check = %v, want the three subscriptions none connected", err)
	}
}

// A subscription list that cannot be read is a failure, not "not in use".
func TestBrokerCheckFailsWhenTheSubscriptionsCannotBeRead(t *testing.T) {
	failing := func(context.Context, string) ([][]byte, error) { return nil, errors.New("permission denied") }
	err := brokerCheck(worker.SinkKindNATS, failing, noConns)(context.Background())
	var unused *health.NotInUseError
	if err == nil || errors.As(err, &unused) || !strings.Contains(err.Error(), "nats subscriptions: permission denied") {
		t.Fatalf("check = %v, want the read failure naming the kind", err)
	}
}
