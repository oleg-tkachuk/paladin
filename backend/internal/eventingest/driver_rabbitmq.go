package eventingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// RabbitMQDriver consumes from an AMQP queue. The queue is assumed to
// exist (declared by the operator out-of-band — typically via the
// RabbitMQ management plugin or a side-car migrator); this driver
// does not own exchange / queue / binding topology.
//
// Acknowledgement: ACK after deliver returns nil. NACK with requeue
// on error so the broker re-delivers; if the same message is
// re-delivered too many times, the operator should configure a
// dead-letter exchange on the queue (broker-side concern).
//
// Reconnect: on connection drop, Run loops and re-Dials with
// exponential backoff up to 30s. The outer ctx kills the loop on
// shutdown.
type RabbitMQDriver struct {
	URL           string
	Queue         string
	PrefetchCount int
	SourceAdapt   Source

	Logger *zap.Logger
}

func (d *RabbitMQDriver) Name() string { return "rabbitmq" }

func (d *RabbitMQDriver) Run(ctx context.Context, deliver func(context.Context, CloudEvent) error) error {
	if d.SourceAdapt == nil {
		return errors.New("rabbitmq driver: no Source adapter configured")
	}
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.runOnce(ctx, deliver); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			d.log().Warn("rabbitmq session ended; will reconnect",
				zap.Error(err),
				zap.Duration("backoff", backoff),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		// runOnce returned nil → ctx was cancelled cleanly.
		return ctx.Err()
	}
}

// runOnce establishes one AMQP session: dial, channel, QoS, consume.
// Returns when the connection drops, the channel closes, or ctx is
// cancelled. On any of those, the outer Run loop re-enters.
func (d *RabbitMQDriver) runOnce(
	ctx context.Context,
	deliver func(context.Context, CloudEvent) error,
) error {
	conn, err := amqp.Dial(d.URL)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	connCloseCh := make(chan *amqp.Error, 1)
	conn.NotifyClose(connCloseCh)

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("amqp channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	prefetch := d.PrefetchCount
	if prefetch <= 0 {
		prefetch = 32
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("amqp qos: %w", err)
	}

	msgs, err := ch.Consume(
		d.Queue,
		"paladin-ingest", // consumer tag
		false,        // autoAck=false — we ack/nack explicitly
		false,        // exclusive
		false,        // noLocal
		false,        // noWait
		nil,          // args
	)
	if err != nil {
		return fmt.Errorf("amqp consume %q: %w", d.Queue, err)
	}

	d.log().Info("rabbitmq consumer started",
		zap.String("queue", d.Queue),
		zap.Int("prefetch", prefetch),
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-connCloseCh:
			return fmt.Errorf("amqp connection closed: %w", err)
		case delivery, ok := <-msgs:
			if !ok {
				return errors.New("amqp consume channel closed")
			}
			d.handle(ctx, delivery, deliver)
		}
	}
}

func (d *RabbitMQDriver) handle(
	ctx context.Context,
	delivery amqp.Delivery,
	deliver func(context.Context, CloudEvent) error,
) {
	ev, err := d.SourceAdapt.Parse(delivery.Body, delivery.ContentType)
	if err != nil {
		if errors.Is(err, ErrIgnoredEvent) {
			_ = delivery.Ack(false) // ack — uninteresting, no retry
			return
		}
		d.log().Warn("rabbitmq source parse failed", zap.Error(err))
		// Reject without requeue — repeated re-delivery of an
		// unparseable message wastes broker effort. Operators who
		// want a DLQ for these set it up on the queue side.
		_ = delivery.Reject(false)
		return
	}

	// Prefer broker-supplied message-id when set (publishers can pin
	// it for end-to-end dedup); fall back to source-adapter id.
	if delivery.MessageId != "" {
		ev.ID = delivery.MessageId
	}

	if err := deliver(ctx, ev); err != nil {
		// requeue=true so RabbitMQ re-delivers per its policy.
		_ = delivery.Nack(false, true)
		return
	}
	_ = delivery.Ack(false)
}

func (d *RabbitMQDriver) log() *zap.Logger {
	if d.Logger == nil {
		return zap.NewNop()
	}
	return d.Logger
}
