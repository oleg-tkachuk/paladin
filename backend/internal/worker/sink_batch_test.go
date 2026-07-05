package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	kafka "github.com/segmentio/kafka-go"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// ─── Kafka batch (outbox fan-in) ─────────────────────────────────────────────

func kafkaBatchItems(t *testing.T, n int, cfg kafkaSinkConfig) []kafkaBatchItem {
	t.Helper()
	items := make([]kafkaBatchItem, n)
	for i := range items {
		items[i] = kafkaBatchItem{
			RowID: uuid.New(),
			Sub:   kafkaTestSub(t, cfg),
			Evt:   Event{Type: "paladin.object.uploaded", TenantID: uuid.NewString()},
		}
	}
	return items
}

func TestDeliverKafkaBatch_OneCallPerGroup(t *testing.T) {
	fake := &fakeKafka{}
	d, _ := kafkaTestDispatcher(fake)
	items := kafkaBatchItems(t, 3, kafkaSinkConfig{Brokers: "b:9092", Topic: "ev"})

	out := d.deliverKafkaBatch(context.Background(), items)

	if fake.calls != 1 {
		t.Fatalf("WriteMessages calls = %d, want 1 (batched into one call)", fake.calls)
	}
	if len(fake.msgs) != 3 {
		t.Fatalf("messages written = %d, want 3", len(fake.msgs))
	}
	for _, it := range items {
		if out[it.RowID] != nil {
			t.Errorf("row %s outcome = %v, want nil", it.RowID, out[it.RowID])
		}
	}
}

func TestDeliverKafkaBatch_PartialFailureMapsPerRow(t *testing.T) {
	// kafka-go reports partial failures as one error per message, in order.
	fake := &fakeKafka{err: kafka.WriteErrors{nil, errors.New("record too large"), nil}}
	d, _ := kafkaTestDispatcher(fake)
	items := kafkaBatchItems(t, 3, kafkaSinkConfig{Brokers: "b:9092", Topic: "ev"})

	out := d.deliverKafkaBatch(context.Background(), items)

	if out[items[0].RowID] != nil || out[items[2].RowID] != nil {
		t.Errorf("rows 0/2 should succeed, got %v / %v", out[items[0].RowID], out[items[2].RowID])
	}
	if out[items[1].RowID] == nil {
		t.Error("row 1 should carry the per-message error")
	}
}

func TestDeliverKafkaBatch_WholeCallFailsAll(t *testing.T) {
	fake := &fakeKafka{err: errors.New("broker down")}
	d, _ := kafkaTestDispatcher(fake)
	items := kafkaBatchItems(t, 3, kafkaSinkConfig{Brokers: "b:9092", Topic: "ev"})

	out := d.deliverKafkaBatch(context.Background(), items)

	for _, it := range items {
		if out[it.RowID] == nil {
			t.Errorf("row %s should fail on a whole-call error", it.RowID)
		}
	}
}

func TestKafkaGroupTarget(t *testing.T) {
	k1, ok1 := kafkaGroupTarget(kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092", Topic: "ev"}))
	k2, ok2 := kafkaGroupTarget(kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092", Topic: "ev"}))
	k3, _ := kafkaGroupTarget(kafkaTestSub(t, kafkaSinkConfig{Brokers: "b:9092", Topic: "other"}))
	if !ok1 || !ok2 || k1 != k2 {
		t.Errorf("identical config must share a group key: %q / %q", k1, k2)
	}
	if k1 == k3 {
		t.Error("different topic must not share a group key")
	}
	if _, ok := kafkaGroupTarget(admindomain.EventSubscription{SinkKind: "http"}); ok {
		t.Error("non-kafka sink must not group as kafka")
	}
	if _, ok := kafkaGroupTarget(kafkaTestSub(t, kafkaSinkConfig{Topic: "ev"})); ok {
		t.Error("missing brokers must not group")
	}
}

// ─── NATS batch (outbox fan-in) ──────────────────────────────────────────────

func natsBatchSub(t *testing.T, url, subject string) admindomain.EventSubscription {
	t.Helper()
	b, _ := json.Marshal(natsSinkConfig{URL: url, Subject: subject})
	return admindomain.EventSubscription{
		SubscriptionID: uuid.New(),
		TenantID:       uuid.New(),
		SinkKind:       "nats",
		SinkConfig:     b,
	}
}

func TestDeliverNATSBatch_PublishesAllOverOneConnection(t *testing.T) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // random, avoid clashing with the other embedded-server tests
	srv := natstest.RunServer(&opts)
	defer srv.Shutdown()
	_ = natsserver.Options{} // keep the import alive (opts's type comes from it)
	url := srv.ClientURL()

	sc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer sc.Close()
	got := make(chan *nats.Msg, 10)
	if _, err := sc.Subscribe("paladin.batch.>", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = sc.Flush()

	d := &Dispatcher{NATS: NewNatsConnPool(nil)}
	defer d.NATS.Close()
	items := []natsBatchItem{
		{RowID: uuid.New(), Sub: natsBatchSub(t, url, "paladin.batch.a"), Evt: Event{Type: "paladin.object.uploaded", At: time.Now()}},
		{RowID: uuid.New(), Sub: natsBatchSub(t, url, "paladin.batch.b"), Evt: Event{Type: "paladin.object.deleted", At: time.Now()}},
		{RowID: uuid.New(), Sub: natsBatchSub(t, url, "paladin.batch.c"), Evt: Event{Type: "paladin.bucket.created", At: time.Now()}},
	}

	out := d.deliverNATSBatch(context.Background(), items)

	for _, it := range items {
		if out[it.RowID] != nil {
			t.Errorf("row %s outcome = %v, want nil", it.RowID, out[it.RowID])
		}
	}
	seen := 0
	deadline := time.After(3 * time.Second)
	for seen < 3 {
		select {
		case <-got:
			seen++
		case <-deadline:
			t.Fatalf("received %d/3 published messages before timeout", seen)
		}
	}
}

func TestDeliverNATSBatch_NoPoolFailsRows(t *testing.T) {
	d := &Dispatcher{} // no NATS pool
	items := []natsBatchItem{{RowID: uuid.New(), Sub: natsBatchSub(t, "nats://x:4222", "s"), Evt: Event{}}}
	out := d.deliverNATSBatch(context.Background(), items)
	if out[items[0].RowID] == nil {
		t.Error("a missing NATS pool must fail the row, not silently succeed")
	}
}

func TestNATSGroupTarget(t *testing.T) {
	// Same (url, creds) but DIFFERENT subjects must still share a group — the
	// flush is per-connection, not per-subject.
	ka, oka := natsGroupTarget(natsBatchSub(t, "nats://s:4222", "sub.a"))
	kb, okb := natsGroupTarget(natsBatchSub(t, "nats://s:4222", "sub.b"))
	kc, _ := natsGroupTarget(natsBatchSub(t, "nats://other:4222", "sub.a"))
	if !oka || !okb || ka != kb {
		t.Errorf("same (url,creds) must share a group regardless of subject: %q / %q", ka, kb)
	}
	if ka == kc {
		t.Error("different url must not share a group")
	}
	if _, ok := natsGroupTarget(admindomain.EventSubscription{SinkKind: "http"}); ok {
		t.Error("non-nats sink must not group as nats")
	}
	if _, ok := natsGroupTarget(natsBatchSub(t, "", "s")); ok {
		t.Error("missing url must not group")
	}
}
