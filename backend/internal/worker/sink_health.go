package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// Sink kinds as event_subscriptions.sink_kind stores them, for the sinks
// whose brokers the dispatcher holds connections to.
const (
	SinkKindNATS     = "nats"
	SinkKindRabbitMQ = "rabbitmq"
)

// BrokerConn is what a sink pool knows of one broker it has dialed: its URL,
// credentials redacted, and Err, nil while connected — a dial that failed,
// or a connection that has dropped since.
type BrokerConn struct {
	URL string
	Err error
}

// errConnectionLost is a pooled connection that is no longer up.
var errConnectionLost = errors.New("connection lost")

// BrokerComponent is the dispatcher's health component for one sink kind.
// Its switch is the database: the kind is in use while an enabled
// subscription delivers to it — subscriptions counts them — and off
// otherwise, its broker never dialed for health. Never critical: a broker
// outage stops only its own sinks.
func BrokerComponent(kind string, subscriptions func(context.Context) (int, error), conns func() []BrokerConn) health.Check {
	return health.Check{
		Name:     kind,
		Category: health.CategoryUpstream,
		Switch: func(ctx context.Context) (health.Enablement, error) {
			n, err := subscriptions(ctx)
			if err != nil {
				return health.Enablement{Control: health.ControlDatabase}, fmt.Errorf("list %s subscriptions: %w", kind, err)
			}
			return health.ByDatabase(n > 0, fmt.Sprintf("no enabled subscription delivers to %s", kind)), nil
		},
		Func: func(context.Context) error { return BrokerHealth(conns()) },
	}
}

// BrokerHealth is the health of a sink kind in use, from what its pool knows:
// every broker connected. No connection at all is not healthy either —
// nothing has reached a broker; a sink whose credentials are a Secret ref
// dials on its first delivery, and says so here until then.
func BrokerHealth(conns []BrokerConn) error {
	var errs []error
	for _, c := range byURL(conns) {
		if c.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.URL, c.Err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if len(conns) == 0 {
		return errNotConnectedYet
	}
	return nil
}

// errNotConnectedYet is a kind in use with no broker dialed.
var errNotConnectedYet = errors.New("no broker connected yet: one is dialed on its first delivery")

// byURL folds the connections to one broker — one per principal or client
// certificate — into one entry, failing if any of them does, sorted by URL so
// a message reads the same on every probe.
func byURL(conns []BrokerConn) []BrokerConn {
	seen := make(map[string]int, len(conns))
	var out []BrokerConn
	for _, c := range conns {
		i, ok := seen[c.URL]
		if !ok {
			seen[c.URL] = len(out)
			out = append(out, c)
			continue
		}
		if out[i].Err == nil {
			out[i].Err = c.Err
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].URL < out[b].URL })
	return out
}
