package eventingest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeSQS returns canned batches then blocks until ctx is cancelled, so a test
// can run the driver against exactly one batch. It records deleted receipt
// handles (the ack channel) and closes `drained` once the batch is exhausted —
// by which point that batch's deliver+delete work is complete.
type fakeSQS struct {
	mu      sync.Mutex
	batches [][]sqstypes.Message
	calls   int
	deleted []string
	recvErr error

	drainOnce sync.Once
	drained   chan struct{}
}

func newFakeSQS(msgs ...sqstypes.Message) *fakeSQS {
	return &fakeSQS{batches: [][]sqstypes.Message{msgs}, drained: make(chan struct{})}
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	if f.recvErr != nil {
		err := f.recvErr
		f.recvErr = nil
		f.mu.Unlock()
		return nil, err
	}
	if f.calls < len(f.batches) {
		b := f.batches[f.calls]
		f.calls++
		f.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: b}, nil
	}
	f.mu.Unlock()
	f.drainOnce.Do(func() { close(f.drained) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	f.mu.Unlock()
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) deletedHandles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func sqsMsg(handle, body string) sqstypes.Message {
	return sqstypes.Message{
		MessageId:     aws.String("mid-" + handle),
		ReceiptHandle: aws.String(handle),
		Body:          aws.String(body),
	}
}

func s3Body(eventName, bucket, key string) string {
	return `{"Records":[{"eventName":"` + eventName +
		`","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"` + bucket +
		`"},"object":{"key":"` + key + `"}}}]}`
}

// driveOnce runs the SQS driver against the fake's single batch, waits until it
// drains, then cancels and waits for Run to return.
func driveOnce(t *testing.T, fake *fakeSQS, d *SQSDriver, deliver func(context.Context, CloudEvent) error) {
	t.Helper()
	d.Client = fake
	if d.QueueURL == "" {
		d.QueueURL = "https://sqs.local/q"
	}
	if d.SourceAdapt == nil {
		d.SourceAdapt = &S3EventSource{URI: "s3://primary"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, deliver) }()

	select {
	case <-fake.drained:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("driver did not drain the batch within 3s")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("driver did not stop after cancel")
	}
}

func TestSQSDriver_ValidEvent_DeliveredAndDeleted(t *testing.T) {
	fake := newFakeSQS(sqsMsg("h1", s3Body("s3:ObjectCreated:Put", "b", "t/ok/k")))
	got := make(chan CloudEvent, 1)
	driveOnce(t, fake, &SQSDriver{}, func(_ context.Context, ev CloudEvent) error {
		got <- ev
		return nil
	})

	select {
	case ev := <-got:
		if ev.Type != EventTypeUploaded || ev.SubjectFields.TenantID != "t" {
			t.Errorf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("event was not delivered")
	}
	if h := fake.deletedHandles(); len(h) != 1 || h[0] != "h1" {
		t.Errorf("deleted = %v, want [h1] (ack on success)", h)
	}
}

func TestSQSDriver_SNSWrapped_Unwrapped(t *testing.T) {
	env, _ := json.Marshal(map[string]string{
		"Type":    "Notification",
		"Message": s3Body("s3:ObjectCreated:Put", "b", "t/ok/k"),
	})
	fake := newFakeSQS(sqsMsg("h1", string(env)))
	got := make(chan CloudEvent, 1)
	driveOnce(t, fake, &SQSDriver{UnwrapSNS: true}, func(_ context.Context, ev CloudEvent) error {
		got <- ev
		return nil
	})

	select {
	case ev := <-got:
		if ev.SubjectFields.Key != "k" {
			t.Errorf("unwrapped event wrong: %+v", ev)
		}
	default:
		t.Fatal("SNS-wrapped event was not unwrapped + delivered")
	}
	if len(fake.deletedHandles()) != 1 {
		t.Error("SNS-wrapped success should delete the message")
	}
}

func TestSQSDriver_IgnoredEvent_DeletedNotDelivered(t *testing.T) {
	// ObjectAccessed → ErrIgnoredEvent → ack (delete), never delivered.
	fake := newFakeSQS(sqsMsg("h1", s3Body("s3:ObjectAccessed:Get", "b", "t/ok/k")))
	var delivered int
	var mu sync.Mutex
	driveOnce(t, fake, &SQSDriver{}, func(context.Context, CloudEvent) error {
		mu.Lock()
		delivered++
		mu.Unlock()
		return nil
	})
	if delivered != 0 {
		t.Errorf("ignored event should not reach deliver (got %d)", delivered)
	}
	if len(fake.deletedHandles()) != 1 {
		t.Error("ignored event should be deleted (acked)")
	}
}

func TestSQSDriver_Unrecognised_DeletedAsPoison(t *testing.T) {
	// An S3 test event / garbage → ErrUnrecognisedEvent → delete (drop poison).
	for _, body := range []string{
		`{"Service":"Amazon S3","Event":"s3:TestEvent"}`, // S3 config test event
		`not json at all`,
	} {
		fake := newFakeSQS(sqsMsg("h1", body))
		var delivered int
		driveOnce(t, fake, &SQSDriver{}, func(context.Context, CloudEvent) error {
			delivered++
			return nil
		})
		if delivered != 0 {
			t.Errorf("body %q: unrecognised should not deliver", body)
		}
		if len(fake.deletedHandles()) != 1 {
			t.Errorf("body %q: unrecognised poison should be deleted", body)
		}
	}
}

func TestSQSDriver_DeliveryError_LeftForRedelivery(t *testing.T) {
	fake := newFakeSQS(sqsMsg("h1", s3Body("s3:ObjectCreated:Put", "b", "t/ok/k")))
	driveOnce(t, fake, &SQSDriver{}, func(context.Context, CloudEvent) error {
		return errors.New("db down") // transient
	})
	if h := fake.deletedHandles(); len(h) != 0 {
		t.Errorf("transient delivery error must NOT delete (got %v) — leave for visibility-timeout redelivery", h)
	}
}

func TestUnwrapSNS(t *testing.T) {
	inner := s3Body("s3:ObjectCreated:Put", "b", "t/ok/k")
	env, _ := json.Marshal(map[string]string{"Type": "Notification", "Message": inner})
	if out, ok := unwrapSNS(env); !ok || string(out) != inner {
		t.Errorf("unwrap Notification: ok=%v out=%s", ok, out)
	}
	// A direct S3→SQS body (not SNS) is left alone.
	if _, ok := unwrapSNS([]byte(inner)); ok {
		t.Error("a bare S3 body should not be treated as SNS")
	}
	// An SNS control message (SubscriptionConfirmation) has no S3 Message.
	if _, ok := unwrapSNS([]byte(`{"Type":"SubscriptionConfirmation","Token":"x"}`)); ok {
		t.Error("SubscriptionConfirmation should not unwrap")
	}
}
