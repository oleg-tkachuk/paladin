// Package sinkkind names the kinds of sink an event subscription delivers
// to, as event_subscriptions.sink_kind stores them. The admin API, the
// dispatcher and the configuration that switches a kind off all spell a kind
// from here.
package sinkkind

const (
	HTTP     = "http"
	NATS     = "nats"
	SQS      = "sqs"
	RabbitMQ = "rabbitmq"
	Kafka    = "kafka"
)

// All lists every kind.
var All = []string{HTTP, NATS, SQS, RabbitMQ, Kafka}
