package eventingest

// Coverage for the NATSDriver core-pubsub path — the mode the lab actually
// runs (jetstream:false). Its JetStream twin is tested next door; this side
// carries the same two decisions and neither was held.
//
// The dedup id is the sharper of them. `Nats-Msg-Id` exists so an event
// re-published by an upstream gateway keeps one identity, and the worker
// writes that id to ingested_events before the handler runs. Read the wrong
// field and a re-publish claims a fresh row: the object is promoted twice and
// the upload fans out to subscribers twice, with nothing reporting an error.
// The same override is written three times in this package — here, in the
// JetStream branch, and in the RabbitMQ driver — and only one had a test.

import (
	"context"
	"sync"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

// startCoreServer boots an in-process nats-server without JetStream — core
// pubsub is all this path needs.
func startCoreServer(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(3 * time.Second) {
		t.Fatal("embedded nats: not ready")
	}
	return srv.ClientURL()
}

// collector gathers the events a driver delivers.
type collector struct {
	mu     sync.Mutex
	events []CloudEvent
}

func (c *collector) deliver(_ context.Context, ev CloudEvent) error {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
	return nil
}

func (c *collector) snapshot() []CloudEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CloudEvent(nil), c.events...)
}

func runCoreDriver(t *testing.T, url, subject, queueGroup string, c *collector) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	d := &NATSDriver{
		URL:        url,
		Subject:    subject,
		QueueGroup: queueGroup,
		SourceAdapt: fakeSource{parse: func(raw []byte, _ string) (CloudEvent, error) {
			return CloudEvent{ID: "from-source", Type: EventTypeUploaded, Data: raw}, nil
		}},
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, c.deliver) }()
	t.Cleanup(func() { cancel(); <-done })

	// The subscription is established inside Run; publishing before it exists
	// silently drops the message on core pubsub, which would make every
	// assertion below vacuous.
	waitFor(t, 3*time.Second,
		func() bool { return d.Status() == nats.CONNECTED }, "driver never connected")
	time.Sleep(50 * time.Millisecond) // subscribe follows connect
}

func TestNATSDriver_Core_MsgIDOverridesSourceID(t *testing.T) {
	url := startCoreServer(t)

	cases := map[string]struct {
		subject string
		header  string
		wantID  string
	}{
		"broker msg-id present": {subject: "core.msgid.present", header: "from-broker", wantID: "from-broker"},
		"broker msg-id absent":  {subject: "core.msgid.absent", header: "", wantID: "from-source"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			subject := tc.subject
			c := &collector{}
			runCoreDriver(t, url, subject, "", c)

			nc, err := nats.Connect(url)
			if err != nil {
				t.Fatalf("publisher connect: %v", err)
			}
			defer nc.Close()

			msg := nats.NewMsg(subject)
			msg.Data = []byte(`{}`)
			if tc.header != "" {
				msg.Header = nats.Header{nats.MsgIdHdr: []string{tc.header}}
			}
			if err := nc.PublishMsg(msg); err != nil {
				t.Fatalf("publish: %v", err)
			}
			_ = nc.Flush()

			waitFor(t, 3*time.Second,
				func() bool { return len(c.snapshot()) == 1 }, "no event delivered")
			if got := c.snapshot()[0].ID; got != tc.wantID {
				t.Errorf("dedup id = %q, want %q", got, tc.wantID)
			}
		})
	}
}

// A queue group is what stops every replica from processing every event. The
// dedup table would catch the duplicates, but only after each replica has
// already claimed a row and raced the others — so the group is the primary
// mechanism and its absence changes the delivery model, not just the cost.
func TestNATSDriver_Core_QueueGroupDeliversOnce(t *testing.T) {
	url := startCoreServer(t)

	t.Run("with a queue group, one subscriber sees it", func(t *testing.T) {
		const subject = "core.qg.grouped"
		a, b := &collector{}, &collector{}
		runCoreDriver(t, url, subject, "ingest", a)
		runCoreDriver(t, url, subject, "ingest", b)

		publishOne(t, url, subject)

		total := func() int { return len(a.snapshot()) + len(b.snapshot()) }
		waitFor(t, 3*time.Second, func() bool { return total() == 1 },
			"no event reached the queue group")
		// Give a second copy time to arrive if the grouping is not in effect.
		time.Sleep(200 * time.Millisecond)
		if total() != 1 {
			t.Errorf("delivered %d copies to a queue group, want 1", total())
		}
	})

	t.Run("without one, every subscriber sees it", func(t *testing.T) {
		const subject = "core.qg.ungrouped"
		a, b := &collector{}, &collector{}
		runCoreDriver(t, url, subject, "", a)
		runCoreDriver(t, url, subject, "", b)

		publishOne(t, url, subject)

		total := func() int { return len(a.snapshot()) + len(b.snapshot()) }
		waitFor(t, 3*time.Second, func() bool { return total() == 2 },
			"plain subscribers must each receive a copy")
	})
}

func publishOne(t *testing.T, url, subject string) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer nc.Close()
	if err := nc.Publish(subject, []byte(`{}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = nc.Flush()
}
