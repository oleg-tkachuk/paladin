package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

type fakeKafka struct {
	msgs   []kafka.Message
	err    error
	closed int
	calls  int // WriteMessages invocations — batching asserts one call per group
}

func (f *fakeKafka) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	f.calls++
	f.msgs = append(f.msgs, msgs...)
	return f.err
}
func (f *fakeKafka) Close() error { f.closed++; return nil }

func kafkaTestDispatcher(fake kafkaWriter) (*Dispatcher, *int) {
	builds := 0
	pool := NewKafkaWriterPool(nil)
	pool.newWriter = func([]string, string, *kafka.Transport) kafkaWriter { builds++; return fake }
	return &Dispatcher{Kafka: pool}, &builds
}

func kafkaTestSub(t *testing.T, cfg kafkaSinkConfig) admindomain.EventSubscription {
	t.Helper()
	b, _ := json.Marshal(cfg)
	return admindomain.EventSubscription{
		SubscriptionID: uuid.New(),
		TenantID:       uuid.New(),
		SinkKind:       "kafka",
		SinkConfig:     b,
	}
}

func TestDeliverKafka_WritesCloudEventEnvelope(t *testing.T) {
	fake := &fakeKafka{}
	d, builds := kafkaTestDispatcher(fake)
	sub := kafkaTestSub(t, kafkaSinkConfig{Brokers: "b1:9092, ,b2:9092", Topic: "paladin-events"})
	if _, err := d.deliverKafka(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverKafka: %v", err)
	}
	if len(fake.msgs) != 1 {
		t.Fatalf("WriteMessages records = %d, want 1", len(fake.msgs))
	}
	m := fake.msgs[0]
	if string(m.Key) != "tenant-123" {
		t.Errorf("message key = %q, want tenant-123 (per-tenant partitioning)", m.Key)
	}
	var env cloudEventEnvelope
	if err := json.Unmarshal(m.Value, &env); err != nil {
		t.Fatalf("value is not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" || env.TenantID != "tenant-123" {
		t.Errorf("envelope mismatch: %+v", env)
	}
	// Second delivery reuses the cached writer (no rebuild).
	if _, err := d.deliverKafka(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverKafka #2: %v", err)
	}
	if *builds != 1 {
		t.Errorf("writer builds = %d, want 1 (pool reuse)", *builds)
	}
}

func TestDeliverKafka_Errors(t *testing.T) {
	ev := sinkTestEvent()
	d, _ := kafkaTestDispatcher(&fakeKafka{})
	if _, err := d.deliverKafka(context.Background(), kafkaTestSub(t, kafkaSinkConfig{Topic: "t"}), ev); err == nil {
		t.Error("missing brokers should error")
	}
	if _, err := d.deliverKafka(context.Background(), kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092"}), ev); err == nil {
		t.Error("missing topic should error")
	}
	dErr, _ := kafkaTestDispatcher(&fakeKafka{err: errors.New("broker down")})
	if _, err := dErr.deliverKafka(context.Background(), kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092", Topic: "t"}), ev); err == nil {
		t.Error("write error should propagate")
	}
	if _, err := (&Dispatcher{}).deliverKafka(context.Background(), kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092", Topic: "t"}), ev); err == nil {
		t.Error("nil Kafka pool should error")
	}
}
