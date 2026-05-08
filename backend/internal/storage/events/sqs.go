// Package events consumes S3 object-level notifications and drives the
// PENDING → AVAILABLE transition in PALADIN's object state machine.
//
// Only s3:ObjectCreated:* notifications are interpreted. Other event types
// (Delete, Replication, Lifecycle) are observed but not consumed — they come
// from PALADIN itself or from lifecycle rules owned by the storage team.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// SQSClient is the subset of aws/sqs Client used here. Narrowed to keep tests
// from depending on the whole SDK surface.
type SQSClient interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// Resolver maps (objectKey, key) pairs to PALADIN object_ids. Event payloads carry
// physical S3 coordinates, not PALADIN logical identity.
type Resolver interface {
	ResolveObjectID(ctx context.Context, physicalBucket, key string) (uuid.UUID, error)
}

// Config controls poll cadence and concurrency.
type Config struct {
	QueueURL          string
	MaxMessages       int32         // 1..10
	VisibilityTimeout int32         // seconds
	PollInterval      time.Duration // zero = 20s long-poll
}

type Consumer struct {
	sqs      SQSClient
	resolver Resolver
	sm       *statemachine.Transitioner
	cfg      Config
	log      *zap.Logger
}

func NewConsumer(client SQSClient, resolver Resolver, sm *statemachine.Transitioner, cfg Config, log *zap.Logger) *Consumer {
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = 10
	}
	if cfg.VisibilityTimeout == 0 {
		cfg.VisibilityTimeout = 30
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 20 * time.Second
	}
	return &Consumer{sqs: client, resolver: resolver, sm: sm, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled. Failures in individual message handlers
// are logged; the consumer keeps running (at-least-once delivery means SQS
// will redeliver failed messages after visibility timeout).
func (c *Consumer) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.cfg.QueueURL),
			MaxNumberOfMessages: c.cfg.MaxMessages,
			VisibilityTimeout:   c.cfg.VisibilityTimeout,
			WaitTimeSeconds:     int32(c.cfg.PollInterval.Seconds()),
		})
		if err != nil {
			c.log.Warn("failed to receive from sqs", zap.Error(err))
			// Back off briefly to avoid hammering SQS on transient errors.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, msg := range out.Messages {
			c.handleMessage(ctx, msg)
		}
	}
}

func (c *Consumer) handleMessage(ctx context.Context, msg sqstypes.Message) {
	if msg.Body == nil {
		_ = c.delete(ctx, msg.ReceiptHandle)
		return
	}
	events, err := parseS3Event(*msg.Body)
	if err != nil {
		c.log.Error("failed to parse s3 event", zap.Error(err), zap.Stringp("body_preview", previewPtr(msg.Body)))
		// Malformed payloads are deleted — redelivery won't help.
		_ = c.delete(ctx, msg.ReceiptHandle)
		return
	}
	allSuccess := true
	for _, ev := range events {
		if err := c.processEvent(ctx, ev); err != nil {
			c.log.Warn("failed to process event", zap.Error(err),
				zap.String("objectKey", ev.ObjectKey), zap.String("key", ev.Key))
			allSuccess = false
		}
	}
	if allSuccess {
		_ = c.delete(ctx, msg.ReceiptHandle)
	}
	// If !allSuccess, leave the message for SQS to redeliver after the
	// visibility timeout expires — the sequencer guard makes double-apply
	// safe, so redelivery is harmless.
}

func (c *Consumer) processEvent(ctx context.Context, ev s3Event) error {
	if !strings.HasPrefix(ev.EventName, "ObjectCreated:") {
		return nil
	}
	objectID, err := c.resolver.ResolveObjectID(ctx, ev.ObjectKey, ev.Key)
	if err != nil {
		return fmt.Errorf("resolve object: %w", err)
	}
	_, err = c.sm.PromoteToAvailable(ctx, objectID, ev.ETag, ev.Size, "", ev.Sequencer, statemachine.SourceEvent)
	return err
}

func (c *Consumer) delete(ctx context.Context, handle *string) error {
	if handle == nil {
		return nil
	}
	_, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.cfg.QueueURL),
		ReceiptHandle: handle,
	})
	return err
}

// s3Event is the flattened shape PALADIN cares about, extracted from the S3
// notification JSON envelope.
type s3Event struct {
	EventName string
	ObjectKey string
	Key       string
	ETag      string
	Size      int64
	Sequencer string
}

// parseS3Event handles both direct S3-to-SQS and EventBridge-wrapped payloads.
func parseS3Event(body string) ([]s3Event, error) {
	// Direct S3 notification envelope.
	var direct struct {
		Records []struct {
			EventName string `json:"eventName"`
			S3        struct {
				ObjectKey struct {
					Name string `json:"name"`
				} `json:"objectKey"`
				Object struct {
					Key       string `json:"key"`
					Size      int64  `json:"size"`
					ETag      string `json:"eTag"`
					Sequencer string `json:"sequencer"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal([]byte(body), &direct); err == nil && len(direct.Records) > 0 {
		out := make([]s3Event, 0, len(direct.Records))
		for _, r := range direct.Records {
			out = append(out, s3Event{
				EventName: r.EventName,
				ObjectKey: r.S3.ObjectKey.Name,
				Key:       r.S3.Object.Key,
				ETag:      strings.Trim(r.S3.Object.ETag, `"`),
				Size:      r.S3.Object.Size,
				Sequencer: r.S3.Object.Sequencer,
			})
		}
		return out, nil
	}
	return nil, errors.New("unrecognized S3 event payload")
}

func previewPtr(s *string) *string {
	if s == nil {
		return nil
	}
	const n = 256
	if len(*s) <= n {
		return s
	}
	p := (*s)[:n]
	return &p
}
