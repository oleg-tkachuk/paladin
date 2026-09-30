package worker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
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
	// AMQPS client-cert auth (all optional, PEM; each also accepts a
	// "k8s:<name>/<key>" Secret ref). Setting cert+key presents a client
	// certificate on the TLS handshake; tls_ca_cert verifies a broker behind
	// a private CA. amqps:// URLs without any of these use system roots.
	TLSClientCert string `json:"tls_client_cert"`
	TLSClientKey  string `json:"tls_client_key"`
	TLSCACert     string `json:"tls_ca_cert"`
}

// buildRabbitTLS mirrors buildKafkaTransport's TLS half: nil when no TLS
// material is configured (amqp.Dial handles plain amqp:// and system-root
// amqps://), a *tls.Config carrying the client keypair / private CA
// otherwise. Errors are loud — dialing with a half-built TLS config would
// surface as an opaque broker handshake failure.
func buildRabbitTLS(cfg rabbitSinkConfig) (*tls.Config, error) {
	if cfg.TLSClientCert == "" && cfg.TLSClientKey == "" && cfg.TLSCACert == "" {
		return nil, nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSClientCert != "" || cfg.TLSClientKey != "" {
		crt, err := tls.X509KeyPair([]byte(cfg.TLSClientCert), []byte(cfg.TLSClientKey))
		if err != nil {
			return nil, fmt.Errorf("rabbitmq sink: client keypair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{crt}
	}
	if cfg.TLSCACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.TLSCACert)) {
			return nil, errors.New("rabbitmq sink: tls_ca_cert contains no valid PEM certificate")
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

// rabbitConnKey namespaces the connection cache by URL + TLS material so
// sinks that share a URL but differ in client certs never share a
// connection. Hashed — never logged.
func rabbitConnKey(cfg rabbitSinkConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", cfg.URL, cfg.TLSClientCert, cfg.TLSClientKey, cfg.TLSCACert)
	return hex.EncodeToString(h.Sum(nil))
}

// RabbitMQConnPool lazily dials + caches one connection per AMQP URL, mirroring
// NatsConnPool. A dropped connection is detected (healthy()) and re-dialed on
// next use. Owned by the dispatcher pod; Close() drains every connection.
type RabbitMQConnPool struct {
	mu   sync.Mutex
	pubs map[string]pooledRabbitPub
	log  *zap.Logger
	// newPub dials url (with the optional TLS config for AMQPS client
	// certs / private CAs) → publisher. Overridable in tests.
	newPub func(url string, tlsCfg *tls.Config) (rabbitPublisher, error)
}

// pooledRabbitPub keeps the human-readable URL next to the publisher so
// Statuses() can report per-broker health without leaking the hashed key.
type pooledRabbitPub struct {
	pub rabbitPublisher
	url string
}

// NewRabbitMQConnPool returns an empty pool whose connections dial on first
// use per URL.
func NewRabbitMQConnPool(log *zap.Logger) *RabbitMQConnPool {
	return &RabbitMQConnPool{
		pubs: map[string]pooledRabbitPub{},
		log:  log,
		newPub: func(url string, tlsCfg *tls.Config) (rabbitPublisher, error) {
			var (
				conn *amqp.Connection
				err  error
			)
			if tlsCfg != nil {
				conn, err = amqp.DialTLS(url, tlsCfg)
			} else {
				// amqp.Dial handles both amqp:// and system-root amqps://.
				conn, err = amqp.Dial(url)
			}
			if err != nil {
				return nil, fmt.Errorf("dial: %w", err)
			}
			return &amqpPublisher{conn: conn}, nil
		},
	}
}

// get returns the cached publisher for `key` (URL + TLS material — see
// rabbitConnKey), dialing via newPub on first use or after a dropped
// connection.
func (p *RabbitMQConnPool) get(key, url string, tlsCfg *tls.Config) (rabbitPublisher, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.pubs[key]; ok {
		if e.pub.healthy() {
			return e.pub, nil
		}
		_ = e.pub.close()
		delete(p.pubs, key)
	}
	pub, err := p.newPub(url, tlsCfg)
	if err != nil {
		return nil, err
	}
	p.pubs[key] = pooledRabbitPub{pub: pub, url: url}
	if p.log != nil {
		p.log.Info("rabbitmq connection dialed",
			zap.String("url", url), zap.Bool("client_tls", tlsCfg != nil))
	}
	return pub, nil
}

// Statuses returns a snapshot of (url → healthy) for every pooled connection.
// The dispatcher's health probe walks this to surface per-broker connectivity
// in /system/health.json. An empty map means no RabbitMQ sink has dialed yet
// (nothing to report), which the probe treats as healthy-but-empty.
func (p *RabbitMQConnPool) Statuses() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool, len(p.pubs))
	for _, e := range p.pubs {
		// Keyed by URL for operator readability; two same-URL sinks with
		// different client certs collapse to one row (AND of health would
		// need a richer shape — fine for a coarse probe).
		out[e.url] = e.pub.healthy()
	}
	return out
}

// Warmup dials each URL once (best-effort) so the health probe has something
// to report before the first delivery — mirrors the NATS pool. Dial failures
// are logged, not returned; the per-row deliver path retries under the row's
// normal budget.
func (p *RabbitMQConnPool) Warmup(urls []string) {
	// Warmup only covers URL-auth sinks: client-cert sinks need their PEM
	// material (possibly a k8s: Secret ref), so they dial lazily on first
	// delivery instead.
	for _, url := range urls {
		if _, err := p.get(rabbitConnKey(rabbitSinkConfig{URL: url}), url, nil); err != nil && p.log != nil {
			p.log.Warn("rabbitmq: pre-warm dial failed",
				zap.String("url", url), zap.Error(err))
		}
	}
}

// Close drains every pooled connection. Safe to call from a defer in the
// dispatcher pod's main.
func (p *RabbitMQConnPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, e := range p.pubs {
		_ = e.pub.close()
		delete(p.pubs, key)
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
	// The AMQP URL embeds the credentials (amqps://user:pass@host/vhost) and
	// the TLS fields carry PEM material — any of them may be a "k8s:" Secret
	// ref (sink_secrets.go). Resolve BEFORE the TLS build and the pool key,
	// so the key covers the resolved material and a rotated Secret dials
	// fresh.
	for _, f := range []*string{&cfg.URL, &cfg.TLSClientCert, &cfg.TLSClientKey, &cfg.TLSCACert} {
		v, rerr := d.resolveSinkValue(ctx, *f)
		if rerr != nil {
			return 0, fmt.Errorf("rabbitmq sink: %w", rerr)
		}
		*f = v
	}
	tlsCfg, err := buildRabbitTLS(cfg)
	if err != nil {
		return 0, err
	}
	pub, err := d.RabbitMQ.get(rabbitConnKey(cfg), cfg.URL, tlsCfg)
	if err != nil {
		return 0, err
	}
	if err := pub.publish(ctx, cfg.Exchange, cfg.RoutingKey, body); err != nil {
		return 0, fmt.Errorf("rabbitmq publish: %w", err)
	}
	return 0, nil
}
