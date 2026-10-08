package config

import "github.com/oleg-tkachuk/paladin/backend/internal/sinkkind"

// sinkSwitchPrefix is where the per-kind switches live in the config tree.
const sinkSwitchPrefix = "dispatcher.sinks."

// SinkSwitchKey is the configuration key that switches kind.
func SinkSwitchKey(kind string) string {
	return sinkSwitchPrefix + kind + ".enabled"
}

// Enabled reports whether kind may be delivered to. A kind with no switch is
// off: nothing would be delivering it, so nothing is allowed to queue it.
func (s DispatcherSinks) Enabled(kind string) bool {
	switch kind {
	case sinkkind.HTTP:
		return s.HTTP.Enabled
	case sinkkind.NATS:
		return s.NATS.Enabled
	case sinkkind.SQS:
		return s.SQS.Enabled
	case sinkkind.RabbitMQ:
		return s.RabbitMQ.Enabled
	case sinkkind.Kafka:
		return s.Kafka.Enabled
	default:
		return false
	}
}
