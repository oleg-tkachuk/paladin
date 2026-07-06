package eventingest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/zap"
)

// sqsMessage aliases the SDK message type so the handle/delete signatures stay
// readable.
type sqsMessage = sqstypes.Message

// SQSReceiver is the subset of the AWS SQS client the driver uses. Narrowed
// (same trick as worker.sqsSender) so tests substitute a fake without the SDK
// or a live queue. The concrete *sqs.Client satisfies it.
type SQSReceiver interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// SQSDriver polls an AWS SQS queue that S3 delivers bucket notifications to —
// the canonical AWS "S3 → SQS" path. It long-polls ReceiveMessage, hands each
// body to the Source adapter (`s3`), and manages the queue lifecycle as the
// ack channel:
//
//   - parse OK + deliver OK → DeleteMessage (ack — done).
//   - ErrIgnoredEvent (not our bucket / uninteresting op) → DeleteMessage
//     (ack — it will never be actionable).
//   - ErrUnrecognisedEvent (S3 s3:TestEvent, an SNS control message, garbage)
//     → DeleteMessage (drop the poison; it can never parse, and leaving it
//     would loop until the redrive policy dead-letters it anyway). Mirrors the
//     RabbitMQ driver's Reject-without-requeue.
//   - deliver error (transient — DB down) → LEAVE the message. It reappears
//     after VisibilityTimeout and the queue's redrive policy dead-letters it
//     after maxReceiveCount. This is the one path that populates a DLQ.
//
// The queue is assumed to exist with its S3 notification + (optional) redrive
// policy configured out-of-band; this driver owns no topology.
//
// Dedup id: the S3 source derives a stable id (x-amz-request-id or a content
// hash) that survives S3 re-delivery, so — unlike NATS/RabbitMQ — we do NOT
// override it with the SQS MessageId (which changes on every enqueue).
type SQSDriver struct {
	Client            SQSReceiver
	QueueURL          string
	MaxMessages       int32 // 1..10; 0 → 10
	WaitTimeSeconds   int32 // 0..20; long-poll
	VisibilityTimeout int32 // seconds; 0 → queue default
	UnwrapSNS         bool
	SourceAdapt       Source

	Logger *zap.Logger
}

func (d *SQSDriver) Name() string { return "sqs" }

func (d *SQSDriver) Run(ctx context.Context, deliver func(context.Context, CloudEvent) error) error {
	if d.SourceAdapt == nil {
		return errors.New("sqs driver: no Source adapter configured")
	}
	if d.Client == nil {
		return errors.New("sqs driver: no SQS client configured")
	}

	maxMsgs := d.MaxMessages
	if maxMsgs <= 0 || maxMsgs > 10 {
		maxMsgs = 10
	}
	waitTime := d.WaitTimeSeconds
	if waitTime < 0 || waitTime > 20 {
		waitTime = 20
	}

	d.log().Info("sqs consumer started",
		zap.String("queue_url", d.QueueURL),
		zap.Int32("max_messages", maxMsgs),
		zap.Int32("wait_time_seconds", waitTime),
		zap.Bool("unwrap_sns", d.UnwrapSNS),
	)

	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		out, err := d.Client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(d.QueueURL),
			MaxNumberOfMessages: maxMsgs,
			WaitTimeSeconds:     waitTime,
			VisibilityTimeout:   d.VisibilityTimeout,
		})
		if err != nil {
			// A cancelled ctx surfaces as a receive error — return it cleanly.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.log().Warn("sqs receive failed; backing off",
				zap.Error(err), zap.Duration("backoff", backoff))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second // reset after a successful receive
		for i := range out.Messages {
			d.handle(ctx, &out.Messages[i], deliver)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}

func (d *SQSDriver) handle(
	ctx context.Context,
	msg *sqsMessage,
	deliver func(context.Context, CloudEvent) error,
) {
	payload := []byte(aws.ToString(msg.Body))
	if d.UnwrapSNS {
		if inner, ok := unwrapSNS(payload); ok {
			payload = inner
		}
	}

	ev, err := d.SourceAdapt.Parse(payload, "application/json")
	if err != nil {
		if errors.Is(err, ErrIgnoredEvent) {
			d.deleteMsg(ctx, msg) // ack — not ours / uninteresting
			return
		}
		// Unrecognised → permanent. Drop the poison so it can't loop.
		d.log().Warn("sqs source parse failed; deleting message", zap.Error(err))
		d.deleteMsg(ctx, msg)
		return
	}

	if err := deliver(ctx, ev); err != nil {
		// Transient — leave the message. It reappears after VisibilityTimeout;
		// the redrive policy dead-letters it after maxReceiveCount.
		d.log().Warn("sqs delivery failed; leaving for redelivery",
			zap.String("event_id", ev.ID), zap.Error(err))
		return
	}
	d.deleteMsg(ctx, msg)
}

// deleteMsg acks a message. A delete failure is logged, not fatal: the message
// reappears after the visibility timeout and dedup collapses the re-processing
// (the id is stable), so a transient DeleteMessage error can't cause a
// double-promote.
func (d *SQSDriver) deleteMsg(ctx context.Context, msg *sqsMessage) {
	if msg.ReceiptHandle == nil {
		return
	}
	if _, err := d.Client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(d.QueueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		d.log().Warn("sqs delete failed; message will redeliver (dedup absorbs it)",
			zap.Error(err))
	}
}

// snsEnvelope is the SNS `Notification` wrapper. When S3 publishes to SNS and
// SNS fans out to SQS, the SQS body is this envelope and the S3 event JSON is
// the (stringified) `Message` field.
type snsEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

// unwrapSNS extracts the inner S3 payload from an SNS Notification envelope.
// Returns ok=false when the body isn't an SNS notification (a direct S3→SQS
// body, or an SNS control message like SubscriptionConfirmation) so the caller
// falls back to the raw body.
func unwrapSNS(body []byte) ([]byte, bool) {
	var e snsEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, false
	}
	if e.Type == "Notification" && e.Message != "" {
		return []byte(e.Message), true
	}
	return nil, false
}

func (d *SQSDriver) log() *zap.Logger {
	if d.Logger == nil {
		return zap.NewNop()
	}
	return d.Logger
}
