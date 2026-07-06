package eventingest

// Integration coverage for the NATSDriver JetStream (at-least-once) path.
// The lab runs the ingest binding in core-pubsub mode (jetstream:false); the
// durable-consumer branch was wired but only ever exercised by hand. These
// tests boot an in-process nats-server WITH JetStream, pre-provision the
// stream the way the broker-side Job does, and assert the ack/nak/term
// semantics the driver relies on for prod at-least-once delivery.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	jsSubject = "seaweedfs.filer"
	jsStream  = "seaweedfs_filer" // must equal streamFromSubject(jsSubject)
	jsDurable = "paladin-ingest-sf"
)

// fakeSource is a Source whose Parse behaviour the test controls.
type fakeSource struct {
	parse func(raw []byte, ct string) (CloudEvent, error)
}

func (f fakeSource) Name() string { return "fake" }
func (f fakeSource) Parse(raw []byte, ct string) (CloudEvent, error) {
	return f.parse(raw, ct)
}

// startJetStreamServer boots an in-process nats-server with JetStream on a
// random port and returns its client URL. Torn down on cleanup.
func startJetStreamServer(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(3 * time.Second) {
		t.Fatal("embedded jetstream nats: not ready")
	}
	_ = natsserver.Options{}
	return srv.ClientURL()
}

// provisionStream creates the stream the driver's consumer attaches to,
// standing in for the out-of-band broker-side provisioning Job.
func provisionStream(t *testing.T, url string) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     jsStream,
		Subjects: []string{jsSubject},
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
}

// publish sends one message onto the ingest subject, optionally with a
// Nats-Msg-Id header (the driver prefers it over the adapter-derived id).
func publish(t *testing.T, url, msgID string, data []byte) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := &nats.Msg{Subject: jsSubject, Data: data}
	if msgID != "" {
		msg.Header = nats.Header{nats.MsgIdHdr: []string{msgID}}
	}
	if _, err := js.PublishMsg(ctx, msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// runDriver launches the JetStream driver in the background and returns a
// cancel func; it stops on cleanup.
func runDriver(t *testing.T, url string, src Source, deliver func(context.Context, CloudEvent) error) {
	t.Helper()
	d := &NATSDriver{
		URL:         url,
		Subject:     jsSubject,
		JetStream:   true,
		DurableName: jsDurable,
		SourceAdapt: src,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Run(ctx, deliver)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("driver did not stop within 3s")
		}
	})
}

// consumerInfo fetches live consumer state for ack assertions.
func consumerInfo(t *testing.T, url string) *jetstream.ConsumerInfo {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cons, err := js.Consumer(ctx, jsStream, jsDurable)
	if err != nil {
		t.Fatalf("consumer lookup: %v", err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatalf("consumer info: %v", err)
	}
	return info
}

func okSource() fakeSource {
	return fakeSource{parse: func(raw []byte, _ string) (CloudEvent, error) {
		return CloudEvent{SpecVersion: "1.0", Type: "paladin.object.uploaded", ID: "adapter-id", Data: raw}, nil
	}}
}

func TestNATSDriver_JetStream_DeliversAndAcks(t *testing.T) {
	url := startJetStreamServer(t)
	provisionStream(t, url)

	got := make(chan CloudEvent, 1)
	runDriver(t, url, okSource(), func(_ context.Context, ev CloudEvent) error {
		got <- ev
		return nil
	})

	publish(t, url, "msg-42", []byte(`{"k":"v"}`))

	select {
	case ev := <-got:
		// The Nats-Msg-Id header wins over the adapter-derived id.
		if ev.ID != "msg-42" {
			t.Errorf("event id = %q, want msg-42 (header override)", ev.ID)
		}
		if string(ev.Data) != `{"k":"v"}` {
			t.Errorf("event data = %q", ev.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered within 5s")
	}

	// After a successful deliver the message is acked — nothing pending or
	// awaiting ack, and no redelivery happened.
	waitFor(t, 5*time.Second, func() bool {
		info := consumerInfo(t, url)
		return info.NumPending == 0 && info.NumAckPending == 0 && info.NumRedelivered == 0
	}, "message not acked (still pending/ack-pending/redelivered)")
}

func TestNATSDriver_JetStream_NakRedelivers(t *testing.T) {
	url := startJetStreamServer(t)
	provisionStream(t, url)

	var mu sync.Mutex
	attempts := 0
	delivered := make(chan int, 4)
	runDriver(t, url, okSource(), func(_ context.Context, _ CloudEvent) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		delivered <- n
		// Fail the first attempt → NAK → JetStream redelivers; succeed after.
		if n == 1 {
			return errors.New("transient")
		}
		return nil
	})

	publish(t, url, "msg-nak", []byte(`{}`))

	// Expect at least two deliveries: the failed one and its redelivery.
	first := waitDeliver(t, delivered)
	second := waitDeliver(t, delivered)
	if first != 1 || second < 2 {
		t.Fatalf("attempts = %d, %d; want a failure then a redelivery", first, second)
	}

	// Eventually the redelivery is acked and the backlog drains.
	waitFor(t, 8*time.Second, func() bool {
		info := consumerInfo(t, url)
		return info.NumPending == 0 && info.NumAckPending == 0
	}, "backlog did not drain after redelivery")
}

func TestNATSDriver_JetStream_IgnoredEventAcked(t *testing.T) {
	url := startJetStreamServer(t)
	provisionStream(t, url)

	ignoreSrc := fakeSource{parse: func(_ []byte, _ string) (CloudEvent, error) {
		return CloudEvent{}, ErrIgnoredEvent
	}}
	delivered := make(chan struct{}, 1)
	runDriver(t, url, ignoreSrc, func(_ context.Context, _ CloudEvent) error {
		delivered <- struct{}{}
		return nil
	})

	publish(t, url, "msg-ignore", []byte(`{"noise":true}`))

	// An ignored event is acked (not retried) and never reaches deliver.
	waitFor(t, 5*time.Second, func() bool {
		info := consumerInfo(t, url)
		return info.NumPending == 0 && info.NumAckPending == 0
	}, "ignored event was not acked")

	select {
	case <-delivered:
		t.Fatal("ignored event should not reach the delivery pipeline")
	default:
	}
}

// waitDeliver reads the next delivery attempt number or fails.
func waitDeliver(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(8 * time.Second):
		t.Fatal("expected a delivery attempt within 8s")
		return 0
	}
}

// waitFor polls cond until true or the deadline, failing with msg.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(msg)
}
