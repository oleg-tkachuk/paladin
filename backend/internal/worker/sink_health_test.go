package worker

import (
	"errors"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// The dispatcher reported a broker healthy whenever its pool held nothing
// failing — with no subscription to it at all, and when every dial had failed.
func TestBrokerHealth(t *testing.T) {
	const kind = SinkKindRabbitMQ
	down := errors.New("connection refused")
	cases := []struct {
		name          string
		subscriptions int
		conns         []BrokerConn
		notInUse      bool
		wantErr       []string
	}{
		{name: "no subscription, nothing dialed", notInUse: true},
		// A broker a deleted subscription used is no reason to probe it.
		{name: "no subscription, a stale connection",
			conns: []BrokerConn{{URL: "amqp://old", Err: down}}, notInUse: true},
		{name: "subscriptions, nothing dialed yet", subscriptions: 2,
			wantErr: []string{"2 enabled subscription(s)", "none connected yet"}},
		{name: "connected", subscriptions: 1, conns: []BrokerConn{{URL: "amqp://a"}}},
		{name: "a failed dial", subscriptions: 1,
			conns:   []BrokerConn{{URL: "amqp://a"}, {URL: "amqp://b", Err: down}},
			wantErr: []string{"amqp://b: connection refused"}},
		{name: "a lost connection", subscriptions: 1,
			conns:   []BrokerConn{{URL: "amqp://a", Err: errConnectionLost}},
			wantErr: []string{"amqp://a: connection lost"}},
		// Two principals on one broker read as one broker, failing if either does.
		{name: "one principal of two failing", subscriptions: 2,
			conns:   []BrokerConn{{URL: "amqp://a"}, {URL: "amqp://a", Err: down}},
			wantErr: []string{"amqp://a: connection refused"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := BrokerHealth(kind, tc.subscriptions, tc.conns)
			var unused *health.NotInUseError
			if got := errors.As(err, &unused); got != tc.notInUse {
				t.Fatalf("BrokerHealth = %v, not in use %v, want %v", err, got, tc.notInUse)
			}
			if tc.notInUse {
				if !strings.Contains(unused.Reason, kind) {
					t.Errorf("reason %q does not name %s", unused.Reason, kind)
				}
				return
			}
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Errorf("BrokerHealth = %v, want healthy", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("BrokerHealth = nil, want %v", tc.wantErr)
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("BrokerHealth = %q, want it to say %q", err, w)
				}
			}
		})
	}
}

// Every failing broker is named, in the same order on every probe.
func TestBrokerHealthNamesEachFailureInOrder(t *testing.T) {
	down := errors.New("down")
	err := BrokerHealth(SinkKindNATS, 2, []BrokerConn{{URL: "nats://b", Err: down}, {URL: "nats://a", Err: down}})
	if got, want := err.Error(), "nats://a: down\nnats://b: down"; got != want {
		t.Errorf("BrokerHealth = %q, want %q", got, want)
	}
}
