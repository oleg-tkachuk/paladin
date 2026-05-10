package eventingest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

// NATSDriver subscribes to a NATS subject and routes received messages
// through the configured Source adapter.
//
// Two operating modes:
//
//   - Core NATS pubsub (JetStream=false): at-most-once. Each message
//     either reaches the worker or is dropped. Acknowledgement is a
//     no-op. Cheap; suitable for environments where missed events
//     are caught by the data-plane Reconciler later.
//
//   - JetStream durable consumer (JetStream=true): at-least-once.
//     The driver creates a pull-based consumer with the supplied
//     DurableName so position survives restarts. ACK is sent after
//     the worker pipeline returns nil; NAK on error so JetStream
//     redelivers (broker-side retry budget governs how many times).
//
// Auth: token + TLS for now. NATS supports many flavours (NKeys,
// JWTs); the common pair covers a working-in-prod path. Secret
// resolution happens in cfg.Ingest.NATS.TokenRef one level up;
// at this layer we receive a resolved string.
type NATSDriver struct {
	URL         string
	Subject     string
	QueueGroup  string
	JetStream   bool
	DurableName string
	Token       string
	TLSConfig   *tls.Config
	SourceAdapt Source // exactly one — one driver, one source format

	Logger *zap.Logger

	// connRef holds the live *nats.Conn while Run is in progress so
	// the ingest pod's /system/health.json can ask "is the subscriber
	// actually connected?". Atomic pointer: nil before Run, non-nil
	// once nats.Connect returns, nil-again after Run unwinds. The
	// `subscriber` health check reads it on every probe.
	connRef atomic.Pointer[nats.Conn]
}

func (d *NATSDriver) Name() string { return "nats" }

// Status reports the connection state of the underlying *nats.Conn
// for the ingest pod's health probes. Returns nats.DISCONNECTED when
// Run hasn't been called yet (or has returned). Safe for concurrent
// reads; the pointer is updated atomically by Run.
func (d *NATSDriver) Status() nats.Status {
	c := d.connRef.Load()
	if c == nil {
		return nats.DISCONNECTED
	}
	return c.Status()
}

func (d *NATSDriver) Run(ctx context.Context, deliver func(context.Context, CloudEvent) error) error {
	if d.SourceAdapt == nil {
		return errors.New("nats driver: no Source adapter configured")
	}
	opts := []nats.Option{
		nats.Name("paladin-ingest"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			d.log().Warn("nats disconnected", zap.Error(err))
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			d.log().Info("nats reconnected", zap.String("url", c.ConnectedUrl()))
		}),
	}
	if d.Token != "" {
		opts = append(opts, nats.Token(d.Token))
	}
	if d.TLSConfig != nil {
		opts = append(opts, nats.Secure(d.TLSConfig))
	}

	nc, err := nats.Connect(d.URL, opts...)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	// Publish the live conn to the health probe + clear it on unwind.
	d.connRef.Store(nc)
	defer d.connRef.Store(nil)
	defer nc.Drain() //nolint:errcheck — best-effort on shutdown

	if d.JetStream {
		return d.runJetStream(ctx, nc, deliver)
	}
	return d.runCorePubSub(ctx, nc, deliver)
}

// runCorePubSub uses NATS's at-most-once subject subscription. ACK is
// implicit (server doesn't track); we just block until ctx is done.
func (d *NATSDriver) runCorePubSub(
	ctx context.Context,
	nc *nats.Conn,
	deliver func(context.Context, CloudEvent) error,
) error {
	handle := func(msg *nats.Msg) {
		ev, err := d.SourceAdapt.Parse(msg.Data, msg.Header.Get("Content-Type"))
		if err != nil {
			if errors.Is(err, ErrIgnoredEvent) {
				return
			}
			d.log().Warn("nats source parse failed", zap.Error(err))
			return
		}
		// Prefer NATS-supplied Msg-Id when set — preserves dedup
		// across re-publish from upstream gateways. Falls back to
		// the source-adapter-derived id (which is also stable for
		// the same logical event).
		if mid := msg.Header.Get(nats.MsgIdHdr); mid != "" {
			ev.ID = mid
		}
		if err := deliver(ctx, ev); err != nil {
			d.log().Warn("nats delivery failed",
				zap.String("event_id", ev.ID),
				zap.Error(err),
			)
		}
	}

	var sub *nats.Subscription
	var err error
	if d.QueueGroup != "" {
		sub, err = nc.QueueSubscribe(d.Subject, d.QueueGroup, handle)
	} else {
		sub, err = nc.Subscribe(d.Subject, handle)
	}
	if err != nil {
		return fmt.Errorf("nats subscribe %q: %w", d.Subject, err)
	}
	d.log().Info("nats core pubsub subscribed",
		zap.String("subject", d.Subject),
		zap.String("queue_group", d.QueueGroup),
	)

	<-ctx.Done()
	_ = sub.Unsubscribe()
	return ctx.Err()
}

// runJetStream sets up a durable pull consumer on the configured
// stream subject. ACK on success, NAK on error — JetStream's redelivery
// machinery handles retry budgets per stream config (set on the
// broker side, not here).
func (d *NATSDriver) runJetStream(
	ctx context.Context,
	nc *nats.Conn,
	deliver func(context.Context, CloudEvent) error,
) error {
	if d.DurableName == "" {
		return errors.New("nats jetstream: durable_name required")
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("jetstream init: %w", err)
	}
	// jetstream.New requires the stream to exist (created out-of-band
	// by the operator). CreateOrUpdateConsumer attaches a durable
	// pull consumer; if a stream with the subject doesn't exist we
	// surface that error so the operator notices.
	cons, err := js.CreateOrUpdateConsumer(ctx, d.streamFromSubject(), jetstream.ConsumerConfig{
		Durable:       d.DurableName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: d.Subject,
		MaxAckPending: 1024,
	})
	if err != nil {
		return fmt.Errorf("jetstream consumer: %w", err)
	}
	d.log().Info("nats jetstream subscribed",
		zap.String("subject", d.Subject),
		zap.String("durable", d.DurableName),
	)

	iter, err := cons.Messages()
	if err != nil {
		return fmt.Errorf("jetstream messages iter: %w", err)
	}
	defer iter.Stop()

	go func() {
		<-ctx.Done()
		iter.Stop()
	}()

	for {
		msg, err := iter.Next()
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("jetstream next: %w", err)
		}
		ev, err := d.SourceAdapt.Parse(msg.Data(), msg.Headers().Get("Content-Type"))
		if err != nil {
			if errors.Is(err, ErrIgnoredEvent) {
				_ = msg.Ack() // not interesting; ack so JS doesn't retry
				continue
			}
			d.log().Warn("jetstream source parse failed", zap.Error(err))
			_ = msg.Term() // unparseable → don't retry, dead-letter
			continue
		}
		if mid := msg.Headers().Get(nats.MsgIdHdr); mid != "" {
			ev.ID = mid
		}
		if err := deliver(ctx, ev); err != nil {
			_ = msg.Nak() // retry per consumer policy
			continue
		}
		_ = msg.Ack()
	}
}

// streamFromSubject derives a stream name from the subject by stripping
// dots ("seaweedfs.filer" → "seaweedfs_filer"). NATS doesn't require
// stream names to follow any pattern; this is purely a sensible default.
// Operators who created the stream with a custom name can override
// via DurableName + a co-located consumer (out of scope for now).
func (d *NATSDriver) streamFromSubject() string {
	out := make([]byte, 0, len(d.Subject))
	for i := 0; i < len(d.Subject); i++ {
		c := d.Subject[i]
		if c == '.' {
			c = '_'
		}
		out = append(out, c)
	}
	return string(out)
}

func (d *NATSDriver) log() *zap.Logger {
	if d.Logger == nil {
		return zap.NewNop()
	}
	return d.Logger
}
