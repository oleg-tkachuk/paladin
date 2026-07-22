package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// The consumer's dependencies are already narrowed to SQSClient / Resolver, so
// everything here runs against in-test fakes. The one path not reachable is the
// final sm.PromoteToAvailable call: Transitioner is a concrete struct whose
// pool is unexported, so a promote needs a database. Every branch up to and
// including the resolve is covered.

// ─── fakes ─────────────────────────────────────────────────────────────────

type fakeSQS struct {
	mu sync.Mutex

	receives  []*sqs.ReceiveMessageInput
	batches   [][]sqstypes.Message // one entry consumed per ReceiveMessage call
	recvErrs  []error              // parallel to batches; nil = no error
	deleted   []string
	deleteErr error
}

func (f *fakeSQS) ReceiveMessage(_ context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receives = append(f.receives, in)
	i := len(f.receives) - 1
	if i < len(f.recvErrs) && f.recvErrs[i] != nil {
		return nil, f.recvErrs[i]
	}
	if i < len(f.batches) {
		return &sqs.ReceiveMessageOutput{Messages: f.batches[i]}, nil
	}
	return &sqs.ReceiveMessageOutput{}, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, f.deleteErr
}

func (f *fakeSQS) deletedHandles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

type fakeResolver struct {
	id    uuid.UUID
	err   error
	calls []struct{ bucket, key string }
}

func (r *fakeResolver) ResolveObjectID(_ context.Context, physicalBucket, key string) (uuid.UUID, error) {
	r.calls = append(r.calls, struct{ bucket, key string }{physicalBucket, key})
	if r.err != nil {
		return uuid.Nil, r.err
	}
	return r.id, nil
}

func newTestConsumer(sqsc SQSClient, res Resolver, cfg Config) *Consumer {
	return NewConsumer(sqsc, res, nil, cfg, zap.NewNop())
}

// directEnvelope builds a direct S3-to-SQS notification body.
func directEnvelope(eventName, bucket, key, etag, sequencer string, size int64) string {
	return fmt.Sprintf(`{"Records":[{"eventName":%q,"s3":{"objectKey":{"name":%q},
        "object":{"key":%q,"size":%d,"eTag":%q,"sequencer":%q}}}]}`,
		eventName, bucket, key, size, etag, sequencer)
}

func msg(body, handle string) sqstypes.Message {
	return sqstypes.Message{Body: aws.String(body), ReceiptHandle: aws.String(handle)}
}

// ─── NewConsumer defaults ──────────────────────────────────────────────────

func TestNewConsumerAppliesDefaults(t *testing.T) {
	c := newTestConsumer(&fakeSQS{}, &fakeResolver{}, Config{QueueURL: "q"})

	if c.cfg.MaxMessages != 10 {
		t.Errorf("MaxMessages = %d, want the 10 default", c.cfg.MaxMessages)
	}
	if c.cfg.VisibilityTimeout != 30 {
		t.Errorf("VisibilityTimeout = %d, want the 30s default", c.cfg.VisibilityTimeout)
	}
	if c.cfg.PollInterval != 20*time.Second {
		t.Errorf("PollInterval = %v, want the 20s long-poll default", c.cfg.PollInterval)
	}
}

func TestNewConsumerKeepsExplicitConfig(t *testing.T) {
	c := newTestConsumer(&fakeSQS{}, &fakeResolver{}, Config{
		QueueURL: "q", MaxMessages: 3, VisibilityTimeout: 90, PollInterval: 5 * time.Second,
	})

	if c.cfg.MaxMessages != 3 || c.cfg.VisibilityTimeout != 90 || c.cfg.PollInterval != 5*time.Second {
		t.Errorf("explicit config must survive defaulting, got %+v", c.cfg)
	}
}

// ─── parseS3Event ──────────────────────────────────────────────────────────

func TestParseS3EventDirectEnvelope(t *testing.T) {
	body := directEnvelope("ObjectCreated:Put", "bkt", "t/o/k", `"abc123"`, "0055AA", 4096)

	evs, err := parseS3Event(body)
	if err != nil {
		t.Fatalf("parseS3Event: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	got := evs[0]
	// The quoted ETag S3 sends must be unquoted, or it never matches the
	// value the adapter's HEAD path stores.
	if got.ETag != "abc123" {
		t.Errorf("ETag = %q, want the unquoted form", got.ETag)
	}
	if got.EventName != "ObjectCreated:Put" || got.ObjectKey != "bkt" || got.Key != "t/o/k" {
		t.Errorf("flattened event = %+v", got)
	}
	if got.Size != 4096 || got.Sequencer != "0055AA" {
		t.Errorf("size/sequencer = %d/%q", got.Size, got.Sequencer)
	}
}

func TestParseS3EventMultipleRecords(t *testing.T) {
	body := `{"Records":[
        {"eventName":"ObjectCreated:Put","s3":{"objectKey":{"name":"b1"},"object":{"key":"k1","size":1,"eTag":"e1","sequencer":"s1"}}},
        {"eventName":"ObjectRemoved:Delete","s3":{"objectKey":{"name":"b2"},"object":{"key":"k2","size":2,"eTag":"e2","sequencer":"s2"}}}]}`

	evs, err := parseS3Event(body)
	if err != nil {
		t.Fatalf("parseS3Event: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[1].EventName != "ObjectRemoved:Delete" {
		t.Errorf("second record = %+v", evs[1])
	}
}

func TestParseS3EventRejectsUnrecognized(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty object", `{}`},
		{"empty records", `{"Records":[]}`},
		{"not json", `nonsense`},
		{"empty string", ``},
		{"json array", `[1,2,3]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseS3Event(tc.body); err == nil {
				t.Fatalf("want an error for %q", tc.body)
			}
		})
	}
}

// ─── previewPtr ────────────────────────────────────────────────────────────

func TestPreviewPtr(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if previewPtr(nil) != nil {
			t.Error("want nil")
		}
	})
	t.Run("short string is returned as-is", func(t *testing.T) {
		s := "short"
		if got := previewPtr(&s); got != &s {
			t.Error("a short body should not be copied")
		}
	})
	// Log lines must not carry an unbounded payload.
	t.Run("long string is truncated to 256", func(t *testing.T) {
		s := strings.Repeat("x", 1000)
		got := previewPtr(&s)
		if len(*got) != 256 {
			t.Errorf("preview length = %d, want 256", len(*got))
		}
	})
	t.Run("exactly 256 is untouched", func(t *testing.T) {
		s := strings.Repeat("y", 256)
		if got := previewPtr(&s); len(*got) != 256 {
			t.Errorf("preview length = %d, want 256", len(*got))
		}
	})
}

// ─── handleMessage ─────────────────────────────────────────────────────────

// A body-less message can never be processed, so it must be dropped rather
// than redelivered forever.
func TestHandleMessageDeletesBodylessMessage(t *testing.T) {
	f := &fakeSQS{}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})

	c.handleMessage(context.Background(), sqstypes.Message{ReceiptHandle: aws.String("h1")})

	if got := f.deletedHandles(); len(got) != 1 || got[0] != "h1" {
		t.Errorf("deleted = %v, want [h1]", got)
	}
}

// Malformed payloads are poison-pills: redelivery cannot help, so they are
// deleted instead of being left to cycle.
func TestHandleMessageDeletesMalformedPayload(t *testing.T) {
	f := &fakeSQS{}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})

	c.handleMessage(context.Background(), msg("not-json", "h2"))

	if got := f.deletedHandles(); len(got) != 1 || got[0] != "h2" {
		t.Errorf("deleted = %v, want [h2]", got)
	}
}

// Non-ObjectCreated events are observed but not consumed; they still succeed,
// so the message is acknowledged.
func TestHandleMessageDeletesIgnoredEventTypes(t *testing.T) {
	f := &fakeSQS{}
	res := &fakeResolver{}
	c := newTestConsumer(f, res, Config{QueueURL: "q"})

	c.handleMessage(context.Background(),
		msg(directEnvelope("ObjectRemoved:Delete", "b", "k", "e", "s", 1), "h3"))

	if got := f.deletedHandles(); len(got) != 1 {
		t.Errorf("an ignored event must still be acknowledged, got %v", got)
	}
	if len(res.calls) != 0 {
		t.Error("an ignored event must not reach the resolver")
	}
}

// A resolve failure is transient (the row may not be committed yet), so the
// message must be LEFT for redelivery — the sequencer guard makes the eventual
// double-apply safe.
func TestHandleMessageKeepsMessageWhenResolveFails(t *testing.T) {
	f := &fakeSQS{}
	res := &fakeResolver{err: errors.New("not found")}
	c := newTestConsumer(f, res, Config{QueueURL: "q"})

	c.handleMessage(context.Background(),
		msg(directEnvelope("ObjectCreated:Put", "b", "k", "e", "s", 1), "h4"))

	if got := f.deletedHandles(); len(got) != 0 {
		t.Errorf("a failed event must not be deleted, got %v", got)
	}
	if len(res.calls) != 1 || res.calls[0].bucket != "b" || res.calls[0].key != "k" {
		t.Errorf("resolver must receive the physical coordinates, got %v", res.calls)
	}
}

// One failure inside a multi-record message must hold the whole message back.
func TestHandleMessageKeepsMessageWhenOneRecordFails(t *testing.T) {
	f := &fakeSQS{}
	res := &fakeResolver{err: errors.New("boom")}
	c := newTestConsumer(f, res, Config{QueueURL: "q"})
	body := `{"Records":[
        {"eventName":"ObjectRemoved:Delete","s3":{"objectKey":{"name":"b"},"object":{"key":"k1"}}},
        {"eventName":"ObjectCreated:Put","s3":{"objectKey":{"name":"b"},"object":{"key":"k2"}}}]}`

	c.handleMessage(context.Background(), msg(body, "h5"))

	if got := f.deletedHandles(); len(got) != 0 {
		t.Errorf("a partially failed batch must not be acknowledged, got %v", got)
	}
}

// ─── delete ────────────────────────────────────────────────────────────────

func TestDeleteIsNoopForNilHandle(t *testing.T) {
	f := &fakeSQS{}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})

	if err := c.delete(context.Background(), nil); err != nil {
		t.Fatalf("nil handle must be a no-op, got %v", err)
	}
	if len(f.deletedHandles()) != 0 {
		t.Error("nil handle must not call SQS")
	}
}

func TestDeleteTargetsTheConfiguredQueue(t *testing.T) {
	f := &fakeSQS{}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "https://sqs/q"})

	if err := c.delete(context.Background(), aws.String("h")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := f.deletedHandles(); len(got) != 1 || got[0] != "h" {
		t.Errorf("deleted = %v", got)
	}
}

func TestDeleteSurfacesSQSError(t *testing.T) {
	boom := errors.New("throttled")
	f := &fakeSQS{deleteErr: boom}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})

	if err := c.delete(context.Background(), aws.String("h")); !errors.Is(err, boom) {
		t.Fatalf("want the SQS error, got %v", err)
	}
}

// ─── Run ───────────────────────────────────────────────────────────────────

func TestRunReturnsOnCancelledContext(t *testing.T) {
	f := &fakeSQS{}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(f.receives) != 0 {
		t.Error("an already-cancelled context must not poll")
	}
}

func TestRunForwardsPollParameters(t *testing.T) {
	f := &fakeSQS{
		batches: [][]sqstypes.Message{{msg(directEnvelope("ObjectRemoved:Delete", "b", "k", "e", "s", 1), "h")}},
	}
	c := newTestConsumer(f, &fakeResolver{}, Config{
		QueueURL: "https://sqs/q", MaxMessages: 5, VisibilityTimeout: 45, PollInterval: 7 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = c.Run(ctx)

	if len(f.receives) == 0 {
		t.Fatal("Run must poll at least once")
	}
	in := f.receives[0]
	if aws.ToString(in.QueueUrl) != "https://sqs/q" {
		t.Errorf("QueueUrl = %v", aws.ToString(in.QueueUrl))
	}
	if in.MaxNumberOfMessages != 5 || in.VisibilityTimeout != 45 {
		t.Errorf("max/visibility = %d/%d, want 5/45", in.MaxNumberOfMessages, in.VisibilityTimeout)
	}
	// PollInterval becomes the long-poll wait, in whole seconds.
	if in.WaitTimeSeconds != 7 {
		t.Errorf("WaitTimeSeconds = %d, want 7", in.WaitTimeSeconds)
	}
}

// A transient receive failure must not spin the loop; the consumer backs off
// and keeps running rather than returning.
func TestRunBacksOffAfterReceiveError(t *testing.T) {
	f := &fakeSQS{recvErrs: []error{errors.New("transient")}}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.Run(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run should end only when the context does, got %v", err)
	}
	// The 2s backoff is longer than the 300ms deadline, so the loop must have
	// been waiting in the backoff select rather than re-polling in a spin.
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("returned after %v — the backoff did not engage", elapsed)
	}
	if len(f.receives) > 2 {
		t.Errorf("spun %d times despite the backoff", len(f.receives))
	}
}

func TestRunProcessesEveryMessageInABatch(t *testing.T) {
	body := directEnvelope("ObjectRemoved:Delete", "b", "k", "e", "s", 1)
	f := &fakeSQS{batches: [][]sqstypes.Message{{
		msg(body, "h1"), msg(body, "h2"), msg(body, "h3"),
	}}}
	c := newTestConsumer(f, &fakeResolver{}, Config{QueueURL: "q"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = c.Run(ctx)

	if got := f.deletedHandles(); len(got) < 3 {
		t.Errorf("every message in the batch must be handled, deleted = %v", got)
	}
}
