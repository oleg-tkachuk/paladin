package eventingest

import "context"

// Driver is the transport-side seam — webhook receiver, NATS subscriber,
// RabbitMQ consumer. Implementations block in Run until ctx is
// cancelled, calling deliver(ev) for each parsed CloudEvent.
//
// Acknowledgement contract: deliver returns the result of the worker
// pipeline (dedup → handler). When the underlying transport supports
// per-message ack (NATS JetStream, RabbitMQ), the driver wraps deliver
// with the broker's ack/nack call:
//
//   - deliver returns nil → ack the message
//   - deliver returns err → nack with redelivery (broker retries)
//
// At-most-once transports (webhook, core NATS pubsub) ignore the
// return value; the caller is expected to retry at the publisher
// side if delivery semantics matter.
type Driver interface {
	// Name identifies the driver for logs / metrics.
	Name() string

	// Run blocks until ctx is cancelled or a fatal transport error
	// occurs. The supplied deliver func processes a single event
	// and returns the dedup+handler outcome. Drivers that batch
	// receive (NATS, RabbitMQ) should NOT batch-call deliver — the
	// dedup write is per-event and must run sequentially under one
	// pgx connection per call.
	Run(ctx context.Context, deliver func(context.Context, CloudEvent) error) error
}

// Source is the format-side seam — SeaweedFS filer event JSON, MinIO
// event notification JSON, raw CloudEvents passthrough. A driver hands
// the raw payload bytes to its configured Source; the Source returns a
// CloudEvent or ErrUnrecognisedEvent / ErrIgnoredEvent.
//
// Source URI is the value the adapter stamps as CloudEvent.Source —
// e.g. "seaweedfs://primary". Lets the operator distinguish events
// from multiple backends in audit logs.
type Source interface {
	// Name identifies the source for logs / metrics.
	Name() string

	// Parse converts raw event bytes into a CloudEvent. The contentType
	// argument is the publisher-supplied Content-Type header (when
	// available) — adapters that handle multiple wire formats branch
	// on it.
	Parse(raw []byte, contentType string) (CloudEvent, error)
}
