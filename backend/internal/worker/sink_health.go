package worker

import (
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

// BrokerHealth is the health of one sink kind on the dispatcher: subscriptions
// is how many enabled subscriptions deliver to it, conns what its pool knows.
//
// No subscription means the broker is not in use, whatever the pool holds.
// Subscriptions but no connection is not healthy either: nothing has reached
// a broker — a sink whose credentials are a Secret ref dials on its first
// delivery, and says so here until then.
func BrokerHealth(kind string, subscriptions int, conns []BrokerConn) error {
	if subscriptions == 0 {
		return health.NotInUse(fmt.Sprintf("no enabled subscription delivers to %s", kind))
	}
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
		return fmt.Errorf("%d enabled subscription(s) deliver to %s, none connected yet: a broker is dialed on its first delivery",
			subscriptions, kind)
	}
	return nil
}

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
