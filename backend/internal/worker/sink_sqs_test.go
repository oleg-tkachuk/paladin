package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

type fakeSQS struct {
	sent    []*sqs.SendMessageInput
	batches []*sqs.SendMessageBatchInput
	failIDs map[string]bool // batch entry Ids to report as Failed
	err     error
}

func (f *fakeSQS) SendMessageBatch(_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	f.batches = append(f.batches, in)
	if f.err != nil {
		return nil, f.err
	}
	out := &sqs.SendMessageBatchOutput{}
	for _, e := range in.Entries {
		id := *e.Id
		if f.failIDs[id] {
			out.Failed = append(out.Failed, sqstypes.BatchResultErrorEntry{
				Id: e.Id, Code: aws.String("InternalError"), Message: aws.String("boom"),
			})
			continue
		}
		out.Successful = append(out.Successful, sqstypes.SendMessageBatchResultEntry{Id: e.Id})
	}
	return out, nil
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.sent = append(f.sent, in)
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{}, nil
}

func sqsTestDispatcher(fake sqsSender) *Dispatcher {
	pool := NewSQSClientPool(nil)
	pool.newClient = func(context.Context, string, string) (sqsSender, error) { return fake, nil }
	return &Dispatcher{SQS: pool}
}

func sqsTestSub(t *testing.T, cfg sqsSinkConfig) admindomain.EventSubscription {
	t.Helper()
	b, _ := json.Marshal(cfg)
	return admindomain.EventSubscription{
		SubscriptionID: uuid.New(),
		TenantID:       uuid.New(),
		SinkKind:       "sqs",
		SinkConfig:     b,
	}
}

// sinkTestEvent is the canonical event used across sink unit tests. Shared by
// the SQS + RabbitMQ tests (same package).
func sinkTestEvent() Event {
	return Event{
		Type:         "paladin.bucket.updated",
		At:           time.Unix(1700000000, 0).UTC(),
		TenantID:     "tenant-123",
		ResourceName: "storageBackends/p/buckets/b",
		ID:           "delivery-row-1",
		Payload:      map[string]any{"k": "v"},
	}
}

func TestDeliverSQS_SendsCloudEventEnvelope(t *testing.T) {
	fake := &fakeSQS{}
	d := sqsTestDispatcher(fake)
	sub := sqsTestSub(t, sqsSinkConfig{
		QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q",
		Region:   "us-east-1",
	})
	if _, err := d.deliverSQS(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverSQS: %v", err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("SendMessage calls = %d, want 1", len(fake.sent))
	}
	in := fake.sent[0]
	if in.QueueUrl == nil || *in.QueueUrl != "https://sqs.us-east-1.amazonaws.com/123/q" {
		t.Errorf("queue url = %v", in.QueueUrl)
	}
	var env cloudEventEnvelope
	if err := json.Unmarshal([]byte(*in.MessageBody), &env); err != nil {
		t.Fatalf("body is not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" ||
		env.TenantID != "tenant-123" || env.ID != "delivery-row-1" {
		t.Errorf("envelope mismatch: %+v", env)
	}
	// Standard (non-FIFO) queue → no group / dedup fields.
	if in.MessageGroupId != nil || in.MessageDeduplicationId != nil {
		t.Error("standard queue must not set FIFO group/dedup ids")
	}
}

func TestDeliverSQS_FIFOSetsGroupAndDedup(t *testing.T) {
	fake := &fakeSQS{}
	d := sqsTestDispatcher(fake)
	sub := sqsTestSub(t, sqsSinkConfig{
		QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q.fifo",
		Region:   "us-east-1",
	})
	if _, err := d.deliverSQS(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverSQS: %v", err)
	}
	in := fake.sent[0]
	if in.MessageGroupId == nil || *in.MessageGroupId != "tenant-123" {
		t.Errorf("FIFO MessageGroupId = %v, want tenant-123 (per-tenant ordering)", in.MessageGroupId)
	}
	if in.MessageDeduplicationId == nil || *in.MessageDeduplicationId != "delivery-row-1" {
		t.Errorf("FIFO MessageDeduplicationId = %v, want delivery-row-1 (stable row id)", in.MessageDeduplicationId)
	}
}

func TestDeliverSQS_Errors(t *testing.T) {
	ev := sinkTestEvent()
	d := sqsTestDispatcher(&fakeSQS{})
	if _, err := d.deliverSQS(context.Background(), sqsTestSub(t, sqsSinkConfig{Region: "r"}), ev); err == nil {
		t.Error("missing queue_url should error")
	}
	if _, err := d.deliverSQS(context.Background(), sqsTestSub(t, sqsSinkConfig{QueueURL: "q"}), ev); err == nil {
		t.Error("missing region should error")
	}
	dErr := sqsTestDispatcher(&fakeSQS{err: errors.New("aws down")})
	if _, err := dErr.deliverSQS(context.Background(), sqsTestSub(t, sqsSinkConfig{QueueURL: "q", Region: "r"}), ev); err == nil {
		t.Error("SendMessage error should propagate")
	}
	if _, err := (&Dispatcher{}).deliverSQS(context.Background(), sqsTestSub(t, sqsSinkConfig{QueueURL: "q", Region: "r"}), ev); err == nil {
		t.Error("nil SQS pool should error")
	}
}

// ─── SendMessageBatch (outbox fan-in) ────────────────────────────────────────

func sqsBatchItems(t *testing.T, cfg sqsSinkConfig, n int) []sqsBatchItem {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	items := make([]sqsBatchItem, n)
	for i := range items {
		id := uuid.Must(uuid.NewV7())
		items[i] = sqsBatchItem{
			RowID: id,
			Sub: admindomain.EventSubscription{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       uuid.Must(uuid.NewV7()),
				SinkKind:       "sqs",
				SinkConfig:     raw,
			},
			Evt: Event{Type: "paladin.bucket.updated", TenantID: "tenant-123", ID: id.String()},
		}
	}
	return items
}

func TestSQSGroupTarget(t *testing.T) {
	raw, _ := json.Marshal(sqsSinkConfig{QueueURL: "https://q/1", Region: "eu-1", RoleArn: "arn:a"})
	k1, cfg, ok := sqsGroupTarget(admindomain.EventSubscription{SinkKind: "sqs", SinkConfig: raw})
	if !ok || cfg.QueueURL != "https://q/1" {
		t.Fatalf("well-formed sqs sink must group, got ok=%v cfg=%+v", ok, cfg)
	}
	raw2, _ := json.Marshal(sqsSinkConfig{QueueURL: "https://q/2", Region: "eu-1", RoleArn: "arn:a"})
	k2, _, _ := sqsGroupTarget(admindomain.EventSubscription{SinkKind: "sqs", SinkConfig: raw2})
	if k1 == k2 {
		t.Error("different queues must land in different groups")
	}
	if _, _, ok := sqsGroupTarget(admindomain.EventSubscription{SinkKind: "http"}); ok {
		t.Error("non-sqs sinks must not group")
	}
	if _, _, ok := sqsGroupTarget(admindomain.EventSubscription{SinkKind: "sqs", SinkConfig: []byte(`{"region":"eu-1"}`)}); ok {
		t.Error("missing queue_url must fall back to the per-row path")
	}
}

func TestDeliverSQSBatch_ChunksAtTen(t *testing.T) {
	fake := &fakeSQS{}
	pool := NewSQSClientPool(nil)
	pool.newClient = func(context.Context, string, string) (sqsSender, error) { return fake, nil }
	d := &Dispatcher{SQS: pool}
	cfg := sqsSinkConfig{QueueURL: "https://q/1", Region: "eu-1"}

	out := d.deliverSQSBatch(context.Background(), cfg, sqsBatchItems(t, cfg, 12))
	if len(fake.batches) != 2 {
		t.Fatalf("batch calls = %d, want 2 (10 + 2)", len(fake.batches))
	}
	if n := len(fake.batches[0].Entries); n != 10 {
		t.Errorf("first chunk entries = %d, want 10", n)
	}
	if n := len(fake.batches[1].Entries); n != 2 {
		t.Errorf("second chunk entries = %d, want 2", n)
	}
	for id, err := range out {
		if err != nil {
			t.Errorf("row %s: unexpected error %v", id, err)
		}
	}
}

func TestDeliverSQSBatch_PartialFailureMapsToRows(t *testing.T) {
	cfg := sqsSinkConfig{QueueURL: "https://q/1", Region: "eu-1"}
	items := sqsBatchItems(t, cfg, 3)
	fake := &fakeSQS{failIDs: map[string]bool{items[1].RowID.String(): true}}
	pool := NewSQSClientPool(nil)
	pool.newClient = func(context.Context, string, string) (sqsSender, error) { return fake, nil }
	d := &Dispatcher{SQS: pool}

	out := d.deliverSQSBatch(context.Background(), cfg, items)
	if out[items[0].RowID] != nil || out[items[2].RowID] != nil {
		t.Error("successful entries must map to nil")
	}
	if out[items[1].RowID] == nil {
		t.Error("failed entry must map to its row's error")
	}
}

func TestDeliverSQSBatch_WholeCallFailureFailsChunk(t *testing.T) {
	cfg := sqsSinkConfig{QueueURL: "https://q/1", Region: "eu-1"}
	items := sqsBatchItems(t, cfg, 2)
	fake := &fakeSQS{err: errors.New("throttled")}
	pool := NewSQSClientPool(nil)
	pool.newClient = func(context.Context, string, string) (sqsSender, error) { return fake, nil }
	d := &Dispatcher{SQS: pool}

	out := d.deliverSQSBatch(context.Background(), cfg, items)
	for _, it := range items {
		if out[it.RowID] == nil {
			t.Errorf("row %s must carry the whole-call error", it.RowID)
		}
	}
}

func TestDeliverSQSBatch_FIFOAttributes(t *testing.T) {
	cfg := sqsSinkConfig{QueueURL: "https://q/1.fifo", Region: "eu-1"}
	items := sqsBatchItems(t, cfg, 1)
	fake := &fakeSQS{}
	pool := NewSQSClientPool(nil)
	pool.newClient = func(context.Context, string, string) (sqsSender, error) { return fake, nil }
	d := &Dispatcher{SQS: pool}

	if out := d.deliverSQSBatch(context.Background(), cfg, items); out[items[0].RowID] != nil {
		t.Fatalf("deliver: %v", out[items[0].RowID])
	}
	e := fake.batches[0].Entries[0]
	if e.MessageGroupId == nil || *e.MessageGroupId != "tenant-123" {
		t.Errorf("FIFO group id = %v, want tenant-123", e.MessageGroupId)
	}
	if e.MessageDeduplicationId == nil || *e.MessageDeduplicationId != items[0].RowID.String() {
		t.Errorf("FIFO dedup id = %v, want the delivery-row id", e.MessageDeduplicationId)
	}
}
