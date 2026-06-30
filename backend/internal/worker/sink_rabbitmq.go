package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// rabbitPublisher is the publish seam the RabbitMQ sink depends on. The
// production impl wraps an *amqp.Connection; tests substitute a fake so the
// delivery logic is exercised without a live broker.
type rabbitPublisher interface {
	publish(ctx context.Context, exchange, routingKey string, body []byte) error
	healthy() bool
	close() error
}

// rabbitSinkConfig is the JSON shape stored in event_subscriptions.sink_config
// for sink_kind='rabbitmq'. Tags match the generated pb.RabbitMqSink struct.
// Credentials ride in the AMQP URL (amqp://user:pass@host:5672/vhost).
type rabbitSinkConfig struct {
	URL        string `json:"url"`
	Exchange   string `json:"exchange"`
	RoutingKey string `json:"routing_key"`
}

// RabbitMQConnPool lazily dials + caches one connection per AMQP URL, mirroring
// NatsConnPool. A dropped connection is detected (healthy()) and re-dialed on
// next use. Owned by the dispatcher pod; Close() drains every connection.
type RabbitMQConnPool struct {
	mu   sync.Mutex
	pubs map[string]rabbitPublisher
	log  *zap.Logger
	// newPub dials url → publisher. Overridable in tests.
	newPub func(url string) (rabbitPublisher, error)
}

// NewRabbitMQConnPool returns an empty pool whose connections dial on first
// use per URL.
func NewRabbitMQConnPool(log *zap.Logger) *RabbitMQConnPool {
	return &RabbitMQConnPool{
		pubs: map[string]rabbitPublisher{},
		log:  log,
		newPub: func(url string) (rabbitPublisher, error) {
			conn, err := amqp.Dial(url)
			if err != nil {
				return nil, fmt.Errorf("dial: %w", err)
			}
			return &amqpPublisher{conn: conn}, nil
		},
	}
}

func (p *RabbitMQConnPool) get(url string) (rabbitPublisher, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pub, ok := p.pubs[url]; ok {
		if pub.healthy() {
			return pub, nil
		}
		_ = pub.close()
		delete(p.pubs, url)
	}
	pub, err := p.newPub(url)
	if err != nil {
		return nil, err
	}
	p.pubs[url] = pub
	if p.log != nil {
		p.log.Info("rabbitmq connection dialed", zap.String("url", url))
	}
	return pub, nil
}

// Close drains every pooled connection. Safe to call from a defer in the
// dispatcher pod's main.
func (p *RabbitMQConnPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for url, pub := range p.pubs {
		_ = pub.close()
		delete(p.pubs, url)
	}
}

// amqpPublisher is the production rabbitPublisher: one cached connection,
// a fresh channel per publish (channels are cheap and not goroutine-safe),
// with publisher confirms so a publish only succeeds on a broker ack.
type amqpPublisher struct {
	conn *amqp.Connection
}

func (a *amqpPublisher) healthy() bool { return a.conn != nil && !a.conn.IsClosed() }

func (a *amqpPublisher) close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

func (a *amqpPublisher) publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	ch, err := a.conn.Channel()
	if err != nil {
		return fmt.Errorf("channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	// Publisher confirms turn the fire-and-forget publish into an acked one,
	// so a row only flips to delivered once the broker has the message.
	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("confirm mode: %w", err)
	}
	conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if ok := conf.Wait(); !ok {
		return errors.New("publish nacked by broker")
	}
	return nil
}

// deliverRabbitMQ publishes the CloudEvents 1.0 envelope to the configured
// exchange/routing-key. Success = a broker confirm (ack). statusCode is 0 —
// non-HTTP sinks report 0 so the outbox/UI treat failures like transport
// errors on HTTP sinks.
func (d *Dispatcher) deliverRabbitMQ(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	if d.RabbitMQ == nil {
		return 0, errors.New("rabbitmq sink: dispatcher has no RabbitMQ pool")
	}
	var cfg rabbitSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return 0, fmt.Errorf("rabbitmq sink: decode config: %w", err)
	}
	if cfg.URL == "" {
		return 0, errors.New("rabbitmq sink: missing url")
	}
	if cfg.RoutingKey == "" {
		return 0, errors.New("rabbitmq sink: missing routing_key")
	}
	body, err := json.Marshal(d.newCloudEventEnvelope(sub, evt))
	if err != nil {
		return 0, fmt.Errorf("rabbitmq sink: marshal envelope: %w", err)
	}
	pub, err := d.RabbitMQ.get(cfg.URL)
	if err != nil {
		return 0, err
	}
	if err := pub.publish(ctx, cfg.Exchange, cfg.RoutingKey, body); err != nil {
		return 0, fmt.Errorf("rabbitmq publish: %w", err)
	}
	return 0, nil
}
