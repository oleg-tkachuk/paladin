package eventingest

import (
	"context"
	"errors"
)

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
// the raw payload bytes to its configured Source; the Source returns the
// message's CloudEvents or ErrUnrecognisedEvent / ErrIgnoredEvent.
//
// Source URI is the value the adapter stamps as CloudEvent.Source —
// e.g. "seaweedfs://primary". Lets the operator distinguish events
// from multiple backends in audit logs.
type Source interface {
	// Name identifies the source for logs / metrics.
	Name() string

	// Parse converts one message's raw bytes into its CloudEvents, in
	// order: one for most formats, one per record of an S3 notification.
	// ErrIgnoredEvent means none of them is Paladin's. The contentType
	// argument is the publisher-supplied Content-Type header (when
	// available) — adapters that handle multiple wire formats branch
	// on it.
	Parse(raw []byte, contentType string) ([]CloudEvent, error)
}

// single is the events of a message whose format carries one.
func single(ev CloudEvent, err error) ([]CloudEvent, error) {
	if err != nil {
		return nil, err
	}
	return []CloudEvent{ev}, nil
}

// deliverEach hands every event of one message to deliver, in order, and
// returns their failures joined. One failing does not stop the rest: the
// message is acked only when all succeeded, and its redelivery finds the
// ones handled already claimed in the dedup table.
func deliverEach(ctx context.Context, evs []CloudEvent, deliver func(context.Context, CloudEvent) error) error {
	var errs []error
	for _, ev := range evs {
		if err := deliver(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// MessageIDSeparator joins a transport's message id to the id of one of
// several events the message carries.
const MessageIDSeparator = "/"

// pinMessageID gives a message's events the id its transport pinned, which
// survives a re-publish the source's own id may not. A message of several
// events keeps each event's id after it, or they would dedup as one.
func pinMessageID(evs []CloudEvent, id string) {
	if id == "" {
		return
	}
	if len(evs) == 1 {
		evs[0].ID = id
		return
	}
	for i := range evs {
		evs[i].ID = id + MessageIDSeparator + evs[i].ID
	}
}
