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
	"strings"
	"sync"

	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// kafkaWriter is the publish seam the Kafka sink depends on. *kafka.Writer
// satisfies it; tests substitute a fake so delivery logic runs without a
// broker.
type kafkaWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// kafkaSinkConfig is the JSON shape stored in event_subscriptions.sink_config
// for sink_kind='kafka'. Tags match the generated pb.KafkaSink struct.
// `brokers` is a comma-separated bootstrap list ("b1:9092,b2:9092").
type kafkaSinkConfig struct {
	Brokers string `json:"brokers"`
	Topic   string `json:"topic"`
	// Auth (all optional). SASLMechanism: "" | plain | scram-sha-256 |
	// scram-sha-512. TLSEnabled wraps the connection in server-verified TLS;
	// TLSClientCert/Key (PEM) present a client cert for mTLS. Inline creds are
	// lab-grade — secret-store resolution is a follow-up.
	SASLMechanism string `json:"sasl_mechanism"`
	SASLUsername  string `json:"sasl_username"`
	SASLPassword  string `json:"sasl_password"`
	TLSEnabled    bool   `json:"tls_enabled"`
	TLSClientCert string `json:"tls_client_cert"`
	TLSClientKey  string `json:"tls_client_key"`
	// TLSCACert (PEM bundle) verifies the broker's server certificate for
	// brokers behind a private CA. Empty = system roots. Implies TLS.
	TLSCACert string `json:"tls_ca_cert"`
}

// KafkaWriterPool lazily builds + caches one writer per (brokers, topic),
// mirroring NatsConnPool. A kafka.Writer manages its own broker connections,
// retries, and batching internally and is safe for concurrent use. Owned by
// the dispatcher pod; Close() flushes + closes every writer at shutdown.
type KafkaWriterPool struct {
	mu      sync.Mutex
	writers map[string]kafkaWriter
	log     *zap.Logger
	// newWriter builds a writer for the given brokers+topic, wiring `transport`
	// (SASL / TLS) when non-nil. Overridable in tests.
	newWriter func(brokers []string, topic string, transport *kafka.Transport) kafkaWriter
}

// NewKafkaWriterPool returns an empty pool. Writers dial lazily on first
// WriteMessages per (brokers, topic, auth) key.
func NewKafkaWriterPool(log *zap.Logger) *KafkaWriterPool {
	return &KafkaWriterPool{
		writers: map[string]kafkaWriter{},
		log:     log,
		newWriter: func(brokers []string, topic string, transport *kafka.Transport) kafkaWriter {
			w := &kafka.Writer{
				Addr:                   kafka.TCP(brokers...),
				Topic:                  topic,
				Balancer:               &kafka.Hash{}, // key-based partitioning → per-key ordering
				AllowAutoTopicCreation: false,
				RequiredAcks:           kafka.RequireAll, // ack from all in-sync replicas
			}
			// Only override the transport for authenticated sinks — a nil
			// *kafka.Transport boxed into the RoundTripper interface would be a
			// non-nil interface holding a nil pointer, breaking plaintext.
			if transport != nil {
				w.Transport = transport
			}
			return w
		},
	}
}

// get returns the cached writer for `key` (which encodes brokers, topic, AND
// auth so distinct-credential sinks never share a connection), building one
// via newWriter on first use.
func (p *KafkaWriterPool) get(key string, brokers []string, topic string, transport *kafka.Transport) kafkaWriter {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.writers[key]; ok {
		return w
	}
	w := p.newWriter(brokers, topic, transport)
	p.writers[key] = w
	if p.log != nil {
		p.log.Info("kafka writer built",
			zap.String("brokers", strings.Join(brokers, ",")),
			zap.String("topic", topic),
			zap.Bool("authenticated", transport != nil))
	}
	return w
}

// buildKafkaTransport turns the sink's auth config into a *kafka.Transport, or
// (nil, nil) for a plaintext sink. Returns an error for an unsupported SASL
// mechanism or a bad mTLS keypair, so a misconfigured sink fails the delivery
// with a clear reason rather than silently degrading to plaintext.
func buildKafkaTransport(cfg kafkaSinkConfig) (*kafka.Transport, error) {
	var mech sasl.Mechanism
	switch cfg.SASLMechanism {
	case "", "none":
		// no SASL
	case "plain":
		mech = plain.Mechanism{Username: cfg.SASLUsername, Password: cfg.SASLPassword}
	case "scram-sha-256":
		m, err := scram.Mechanism(scram.SHA256, cfg.SASLUsername, cfg.SASLPassword)
		if err != nil {
			return nil, fmt.Errorf("kafka sink: scram-sha-256: %w", err)
		}
		mech = m
	case "scram-sha-512":
		m, err := scram.Mechanism(scram.SHA512, cfg.SASLUsername, cfg.SASLPassword)
		if err != nil {
			return nil, fmt.Errorf("kafka sink: scram-sha-512: %w", err)
		}
		mech = m
	default:
		return nil, fmt.Errorf("kafka sink: unsupported sasl_mechanism %q", cfg.SASLMechanism)
	}

	var tlsCfg *tls.Config
	if cfg.TLSEnabled || cfg.TLSClientCert != "" || cfg.TLSClientKey != "" || cfg.TLSCACert != "" {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSClientCert != "" || cfg.TLSClientKey != "" {
			crt, err := tls.X509KeyPair([]byte(cfg.TLSClientCert), []byte(cfg.TLSClientKey))
			if err != nil {
				return nil, fmt.Errorf("kafka sink: mTLS keypair: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{crt}
		}
		if cfg.TLSCACert != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(cfg.TLSCACert)) {
				return nil, errors.New("kafka sink: tls_ca_cert contains no valid PEM certificate")
			}
			tlsCfg.RootCAs = pool
		}
	}

	if mech == nil && tlsCfg == nil {
		return nil, nil // plaintext
	}
	return &kafka.Transport{SASL: mech, TLS: tlsCfg}, nil
}

// kafkaWriterKey namespaces the writer cache by brokers + topic + the full
// auth material, so two sinks that share brokers/topic but differ in
// credentials (or plaintext-vs-TLS) get separate writers. The credential
// bytes are hashed (never logged) — the key is in-process only.
func kafkaWriterKey(brokers []string, cfg kafkaSinkConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%s\x00%s\x00%s",
		strings.Join(brokers, ","), cfg.Topic, cfg.SASLMechanism, cfg.SASLUsername,
		cfg.SASLPassword, cfg.TLSEnabled, cfg.TLSClientCert, cfg.TLSClientKey, cfg.TLSCACert)
	return hex.EncodeToString(h.Sum(nil))
}

// Close flushes + closes every pooled writer. Safe to defer in the dispatcher
// pod's main.
func (p *KafkaWriterPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, w := range p.writers {
		_ = w.Close()
		delete(p.writers, key)
	}
}

// deliverKafka publishes the CloudEvents 1.0 envelope to the configured topic.
// The message key is the tenant id so a tenant's events land on one partition
// (per-tenant ordering). RequireAll acks make a successful WriteMessages mean
// the brokers durably accepted the record. statusCode is 0 — non-HTTP sinks
// report 0 so the outbox/UI treat failures like transport errors.
func (d *Dispatcher) deliverKafka(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	if d.Kafka == nil {
		return 0, errors.New("kafka sink: dispatcher has no Kafka writer pool")
	}
	var cfg kafkaSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return 0, fmt.Errorf("kafka sink: decode config: %w", err)
	}
	if cfg.Brokers == "" {
		return 0, errors.New("kafka sink: missing brokers")
	}
	if cfg.Topic == "" {
		return 0, errors.New("kafka sink: missing topic")
	}
	body, err := json.Marshal(d.newCloudEventEnvelope(sub, evt))
	if err != nil {
		return 0, fmt.Errorf("kafka sink: marshal envelope: %w", err)
	}
	brokers := splitTrim(cfg.Brokers)
	if len(brokers) == 0 {
		return 0, errors.New("kafka sink: no usable broker in brokers list")
	}
	// Credential fields may be "k8s:" Secret refs (sink_secrets.go). Resolve
	// them BEFORE the transport build and the pool-key hash: the key is then
	// computed over the resolved material, so a rotated Secret naturally
	// hashes to a new key and gets a freshly-dialed writer.
	for _, f := range []*string{&cfg.SASLUsername, &cfg.SASLPassword, &cfg.TLSClientCert, &cfg.TLSClientKey, &cfg.TLSCACert} {
		v, rerr := d.resolveSinkValue(ctx, *f)
		if rerr != nil {
			return 0, fmt.Errorf("kafka sink: %w", rerr)
		}
		*f = v
	}
	transport, err := buildKafkaTransport(cfg)
	if err != nil {
		return 0, err
	}
	w := d.Kafka.get(kafkaWriterKey(brokers, cfg), brokers, cfg.Topic, transport)
	if err := w.WriteMessages(ctx, kafka.Message{
		Key:   []byte(evt.TenantID),
		Value: body,
	}); err != nil {
		return 0, fmt.Errorf("kafka write: %w", err)
	}
	return 0, nil
}

// ─── Batched delivery (outbox fan-in) ────────────────────────────────────────

// kafkaBatchItem is one outbox row headed for a shared Kafka writer.
type kafkaBatchItem struct {
	RowID uuid.UUID
	Sub   admindomain.EventSubscription
	Evt   Event
}

// kafkaGroupTarget derives the batch-group key for a subscription IFF it is a
// well-formed Kafka sink. Rows sharing brokers+topic+auth flush through one
// WriteMessages call. The key is computed over the RAW (pre-resolution) config,
// so it never merges sinks that differ in credentials (it may under-batch two
// refs that resolve equal — safe). Malformed rows return ok=false and take the
// per-row deliver path, failing with the same error text as before batching.
func kafkaGroupTarget(sub admindomain.EventSubscription) (key string, ok bool) {
	if sub.SinkKind != "kafka" {
		return "", false
	}
	var cfg kafkaSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return "", false
	}
	brokers := splitTrim(cfg.Brokers)
	if len(brokers) == 0 || cfg.Topic == "" {
		return "", false
	}
	return kafkaWriterKey(brokers, cfg), true
}

// deliverKafkaBatch writes one group's rows to a single writer via one
// WriteMessages(msgs...) call (kafka-go batches to the broker internally),
// returning a per-row outcome (nil = delivered). kafka-go reports partial
// failures as a WriteErrors (one entry per message); any other error fails the
// whole group (retryable). All items share brokers/topic/auth by construction,
// so config + credentials are read from the first item.
func (d *Dispatcher) deliverKafkaBatch(ctx context.Context, items []kafkaBatchItem) map[uuid.UUID]error {
	out := make(map[uuid.UUID]error, len(items))
	failAll := func(err error) map[uuid.UUID]error {
		for _, it := range items {
			out[it.RowID] = err
		}
		return out
	}
	if d.Kafka == nil {
		return failAll(errors.New("kafka sink: dispatcher has no Kafka writer pool"))
	}
	var cfg kafkaSinkConfig
	if err := json.Unmarshal(items[0].Sub.SinkConfig, &cfg); err != nil {
		return failAll(fmt.Errorf("kafka sink: decode config: %w", err))
	}
	brokers := splitTrim(cfg.Brokers)
	for _, f := range []*string{&cfg.SASLUsername, &cfg.SASLPassword, &cfg.TLSClientCert, &cfg.TLSClientKey, &cfg.TLSCACert} {
		v, rerr := d.resolveSinkValue(ctx, *f)
		if rerr != nil {
			return failAll(fmt.Errorf("kafka sink: %w", rerr))
		}
		*f = v
	}
	transport, err := buildKafkaTransport(cfg)
	if err != nil {
		return failAll(err)
	}
	w := d.Kafka.get(kafkaWriterKey(brokers, cfg), brokers, cfg.Topic, transport)

	msgs := make([]kafka.Message, 0, len(items))
	rowByIdx := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		body, berr := json.Marshal(d.newCloudEventEnvelope(it.Sub, it.Evt))
		if berr != nil {
			out[it.RowID] = fmt.Errorf("kafka sink: marshal envelope: %w", berr)
			continue
		}
		msgs = append(msgs, kafka.Message{Key: []byte(it.Evt.TenantID), Value: body})
		rowByIdx = append(rowByIdx, it.RowID)
	}
	if len(msgs) == 0 {
		return out
	}
	werr := w.WriteMessages(ctx, msgs...)
	if werr == nil {
		for _, id := range rowByIdx {
			out[id] = nil
		}
		return out
	}
	// Partial failure: kafka-go returns one error per message in order.
	var we kafka.WriteErrors
	if errors.As(werr, &we) && len(we) == len(rowByIdx) {
		for i, id := range rowByIdx {
			if we[i] != nil {
				out[id] = fmt.Errorf("kafka write: %w", we[i])
			} else {
				out[id] = nil
			}
		}
		return out
	}
	// Whole-call failure → every message fails (retryable).
	for _, id := range rowByIdx {
		out[id] = fmt.Errorf("kafka write: %w", werr)
	}
	return out
}

// splitTrim splits a comma-separated list and drops empty / whitespace-only
// entries (so "b1:9092, ,b2:9092" → ["b1:9092","b2:9092"]).
func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
