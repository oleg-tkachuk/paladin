package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// The dispatcher reported a broker healthy whenever its pool held nothing
// failing — with no subscription to it at all, and when every dial had failed.
func TestBrokerComponent(t *testing.T) {
	down := errors.New("connection refused")
	cases := []struct {
		name          string
		subscriptions int
		conns         []BrokerConn
		want          health.ComponentStatus
		wantMessage   string
	}{
		{name: "no subscription, nothing dialed", want: health.StatusDisabled,
			wantMessage: "not in use: no enabled subscription delivers to rabbitmq"},
		// A broker a deleted subscription used is no reason to probe it.
		{name: "no subscription, a stale connection", conns: []BrokerConn{{URL: "amqp://old", Err: down}},
			want: health.StatusDisabled, wantMessage: "not in use"},
		{name: "subscriptions, nothing dialed yet", subscriptions: 2,
			want: health.StatusUnhealthy, wantMessage: "no broker connected yet"},
		{name: "connected", subscriptions: 1, conns: []BrokerConn{{URL: "amqp://a"}}, want: health.StatusHealthy},
		{name: "a failed dial", subscriptions: 1,
			conns: []BrokerConn{{URL: "amqp://a"}, {URL: "amqp://b", Err: down}},
			want:  health.StatusUnhealthy, wantMessage: "amqp://b: connection refused"},
		{name: "a lost connection", subscriptions: 1, conns: []BrokerConn{{URL: "amqp://a", Err: errConnectionLost}},
			want: health.StatusUnhealthy, wantMessage: "amqp://a: connection lost"},
		// Two principals on one broker read as one broker, failing if either does.
		{name: "one principal of two failing", subscriptions: 2,
			conns: []BrokerConn{{URL: "amqp://a"}, {URL: "amqp://a", Err: down}},
			want:  health.StatusUnhealthy, wantMessage: "amqp://a: connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			count := func(context.Context) (int, error) { return tc.subscriptions, nil }
			c := BrokerComponent(SinkKindRabbitMQ, count, func() []BrokerConn { return tc.conns })
			got := (&health.Handler{Ready: []health.Probe{c}}).Snapshot(context.Background(), "dispatcher").Components[0]
			if got.Status != tc.want || !strings.Contains(got.Message, tc.wantMessage) || got.Control != health.ControlDatabase {
				t.Errorf("component = %+v, want %s with %q, switched by the database", got, tc.want, tc.wantMessage)
			}
		})
	}
}

// A subscription list that cannot be read is a failure, not "not in use",
// and no broker is checked.
func TestBrokerComponentFailsWhenTheSubscriptionsCannotBeRead(t *testing.T) {
	count := func(context.Context) (int, error) { return 0, errors.New("permission denied") }
	c := BrokerComponent(SinkKindNATS, count, func() []BrokerConn {
		t.Error("the pool was read with the switch unknown")
		return nil
	})
	got := (&health.Handler{Ready: []health.Probe{c}}).Snapshot(context.Background(), "dispatcher").Components[0]
	if got.Status != health.StatusUnhealthy || !strings.Contains(got.Message, "nats subscriptions: permission denied") {
		t.Errorf("component = %+v, want the read failure naming the kind", got)
	}
}

// Every failing broker is named, in the same order on every probe.
func TestBrokerHealthNamesEachFailureInOrder(t *testing.T) {
	down := errors.New("down")
	err := BrokerHealth([]BrokerConn{{URL: "nats://b", Err: down}, {URL: "nats://a", Err: down}})
	if got, want := err.Error(), "nats://a: down\nnats://b: down"; got != want {
		t.Errorf("BrokerHealth = %q, want %q", got, want)
	}
}
