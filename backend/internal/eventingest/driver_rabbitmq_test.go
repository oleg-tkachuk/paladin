package eventingest

import (
	"context"
	"errors"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

// The acknowledgement decision is the whole of at-least-once delivery, and
// every way of getting it wrong is silent:
//
//   - Ack on a failure drops the event. The broker considers it delivered,
//     nothing retries, and the object stays PENDING forever.
//   - Nack-with-requeue on a message that can never parse spins the broker
//     on it indefinitely, and the queue stops draining behind it.
//   - Reject on a transient failure discards work that would have succeeded.
//
// None of these produce an error anywhere in Paladin; the driver returns
// nothing and the pipeline reports success. The file had no test at all.

// recordingAck stands in for the AMQP channel. amqp.Delivery reaches its
// broker through the exported Acknowledger interface, so the decision is
// observable without one.
type recordingAck struct {
	acks     int
	nacks    int
	rejects  int
	requeued bool
}

func (r *recordingAck) Ack(uint64, bool) error { r.acks++; return nil }
func (r *recordingAck) Nack(_ uint64, _, requeue bool) error {
	r.nacks++
	r.requeued = requeue
	return nil
}
func (r *recordingAck) Reject(_ uint64, requeue bool) error {
	r.rejects++
	r.requeued = requeue
	return nil
}

// sourceFunc adapts a function to the Source seam.
type sourceFunc func(raw []byte, contentType string) (CloudEvent, error)

func (f sourceFunc) Name() string { return "test-source" }
func (f sourceFunc) Parse(raw []byte, ct string) (CloudEvent, error) {
	return f(raw, ct)
}

func TestRabbitMQDriver_AcknowledgementContract(t *testing.T) {
	parsed := CloudEvent{ID: "from-source", Type: EventTypeUploaded}
	boom := errors.New("handler down")

	cases := map[string]struct {
		parse       func([]byte, string) (CloudEvent, error)
		deliverErr  error
		wantAcks    int
		wantNacks   int
		wantRejects int
		wantRequeue bool
		why         string
	}{
		"handled": {
			parse:    func([]byte, string) (CloudEvent, error) { return parsed, nil },
			wantAcks: 1,
			why:      "a processed event must leave the queue",
		},
		"ignored by the source": {
			parse:    func([]byte, string) (CloudEvent, error) { return CloudEvent{}, ErrIgnoredEvent },
			wantAcks: 1,
			why:      "an uninteresting event is done with, not retried",
		},
		"unparseable": {
			parse:       func([]byte, string) (CloudEvent, error) { return CloudEvent{}, errors.New("bad json") },
			wantRejects: 1,
			wantRequeue: false,
			why:         "a message that cannot parse will not parse on redelivery either",
		},
		"handler failed": {
			parse:       func([]byte, string) (CloudEvent, error) { return parsed, nil },
			deliverErr:  boom,
			wantNacks:   1,
			wantRequeue: true,
			why:         "a transient failure must come back, or the event is lost",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ack := &recordingAck{}
			d := &RabbitMQDriver{SourceAdapt: sourceFunc(tc.parse)}
			d.handle(
				context.Background(),
				amqp.Delivery{Acknowledger: ack, Body: []byte(`{}`)},
				func(context.Context, CloudEvent) error { return tc.deliverErr },
			)

			if ack.acks != tc.wantAcks || ack.nacks != tc.wantNacks || ack.rejects != tc.wantRejects {
				t.Fatalf("acks=%d nacks=%d rejects=%d, want %d/%d/%d — %s",
					ack.acks, ack.nacks, ack.rejects,
					tc.wantAcks, tc.wantNacks, tc.wantRejects, tc.why)
			}
			if (tc.wantNacks > 0 || tc.wantRejects > 0) && ack.requeued != tc.wantRequeue {
				t.Errorf("requeue = %v, want %v — %s", ack.requeued, tc.wantRequeue, tc.why)
			}
		})
	}
}

// The broker's message-id wins over the source adapter's, because a publisher
// that pins it is asserting end-to-end identity across replays. The id is the
// dedup key: take it from the wrong place and a re-delivered event claims a
// fresh row, so the handler runs twice and the upload fans out twice.
func TestRabbitMQDriver_MessageIDOverridesSourceID(t *testing.T) {
	parse := func([]byte, string) (CloudEvent, error) {
		return CloudEvent{ID: "from-source", Type: EventTypeUploaded}, nil
	}

	for name, tc := range map[string]struct {
		messageID string
		wantID    string
	}{
		"broker id present": {messageID: "from-broker", wantID: "from-broker"},
		"broker id absent":  {messageID: "", wantID: "from-source"},
	} {
		t.Run(name, func(t *testing.T) {
			var got CloudEvent
			d := &RabbitMQDriver{SourceAdapt: sourceFunc(parse)}
			d.handle(
				context.Background(),
				amqp.Delivery{Acknowledger: &recordingAck{}, MessageId: tc.messageID, Body: []byte(`{}`)},
				func(_ context.Context, ev CloudEvent) error { got = ev; return nil },
			)
			if got.ID != tc.wantID {
				t.Errorf("dedup id = %q, want %q", got.ID, tc.wantID)
			}
		})
	}
}

// Run refuses to start without a Source rather than dialling and then failing
// on every message — the driver would otherwise consume and reject the whole
// queue.
func TestRabbitMQDriver_RunRequiresASource(t *testing.T) {
	d := &RabbitMQDriver{}
	err := d.Run(context.Background(), func(context.Context, CloudEvent) error { return nil })
	if err == nil {
		t.Fatal("Run started with no Source adapter")
	}
	if !strings.Contains(err.Error(), "Source") {
		t.Fatalf("err = %v, want it to name the missing Source adapter", err)
	}
}

func TestRabbitMQDriver_LogIsNilSafe(t *testing.T) {
	d := &RabbitMQDriver{}
	if d.log() == nil {
		t.Fatal("log() returned nil")
	}
	d.log().Warn("must not panic")
	if got := d.Name(); got != "rabbitmq" {
		t.Errorf("Name() = %q, want rabbitmq", got)
	}
}
