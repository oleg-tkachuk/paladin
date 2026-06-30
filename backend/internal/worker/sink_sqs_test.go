package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

type fakeSQS struct {
	sent []*sqs.SendMessageInput
	err  error
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
	pool.newClient = func(context.Context, string) (sqsSender, error) { return fake, nil }
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
