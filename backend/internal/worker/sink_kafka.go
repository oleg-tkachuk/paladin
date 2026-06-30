package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	kafka "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
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
}

// KafkaWriterPool lazily builds + caches one writer per (brokers, topic),
// mirroring NatsConnPool. A kafka.Writer manages its own broker connections,
// retries, and batching internally and is safe for concurrent use. Owned by
// the dispatcher pod; Close() flushes + closes every writer at shutdown.
type KafkaWriterPool struct {
	mu      sync.Mutex
	writers map[string]kafkaWriter
	log     *zap.Logger
	// newWriter builds a writer for the given brokers+topic. Overridable in
	// tests.
	newWriter func(brokers []string, topic string) kafkaWriter
}

// NewKafkaWriterPool returns an empty pool. Writers dial lazily on first
// WriteMessages per (brokers, topic) key.
func NewKafkaWriterPool(log *zap.Logger) *KafkaWriterPool {
	return &KafkaWriterPool{
		writers: map[string]kafkaWriter{},
		log:     log,
		newWriter: func(brokers []string, topic string) kafkaWriter {
			return &kafka.Writer{
				Addr:                   kafka.TCP(brokers...),
				Topic:                  topic,
				Balancer:               &kafka.Hash{}, // key-based partitioning → per-key ordering
				AllowAutoTopicCreation: false,
				RequiredAcks:           kafka.RequireAll, // ack from all in-sync replicas
			}
		},
	}
}

func (p *KafkaWriterPool) get(brokers []string, topic string) kafkaWriter {
	key := strings.Join(brokers, ",") + "|" + topic
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.writers[key]; ok {
		return w
	}
	w := p.newWriter(brokers, topic)
	p.writers[key] = w
	if p.log != nil {
		p.log.Info("kafka writer built", zap.String("brokers", strings.Join(brokers, ",")), zap.String("topic", topic))
	}
	return w
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
	w := d.Kafka.get(brokers, cfg.Topic)
	if err := w.WriteMessages(ctx, kafka.Message{
		Key:   []byte(evt.TenantID),
		Value: body,
	}); err != nil {
		return 0, fmt.Errorf("kafka write: %w", err)
	}
	return 0, nil
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
