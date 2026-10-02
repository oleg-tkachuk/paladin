//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// runEmbeddedJetStream boots an in-process nats-server with JetStream enabled
// and returns its client URL.
func runEmbeddedJetStream(t *testing.T) string {
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
		t.Fatalf("embedded jetstream: not ready")
	}
	return srv.ClientURL()
}

// TestDispatcher_JetStreamSink_PublishesAndDedups pins the JetStream sink mode
// (ADR: NATS JetStream as the event bus): a NATS sink with jetstream=true
// publishes the CloudEvents envelope into a provisioned stream with
// Nats-Msg-Id = the CloudEvents id, so (a) the message is durably stored on the
// stream for downstream consumers to read + replay, and (b) an at-least-once
// redelivery of the SAME event is deduped server-side to a single stream
// message.
func TestDispatcher_JetStreamSink_PublishesAndDedups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := runEmbeddedJetStream(t)

	// Provision the stream the sink publishes onto.
	conn, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:       "PALADIN_EVENTS",
		Subjects:   []string{"paladin.events.>"},
		Duplicates: 2 * time.Minute, // dedup window keyed on Nats-Msg-Id
	})
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	// A jetstream-mode NATS sink pointing at a subject under the stream filter.
	const subject = "paladin.events.test"
	sinkCfg, _ := json.Marshal(map[string]any{
		"url":       url,
		"subject":   subject,
		"jetstream": true,
	})
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       "nats",
		SinkConfig:     sinkCfg,
	}

	pool := worker.NewNatsConnPool(nil)
	defer pool.Close()
	d := &worker.Dispatcher{NATS: pool}

	// Deliver the event — it must land on the stream.
	if err := d.DeliverOne(ctx, sub, "paladin.object.uploaded"); err != nil {
		t.Fatalf("DeliverOne (jetstream): %v", err)
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stream has %d messages, want 1 after first deliver", info.State.Msgs)
	}

	// Inspect the stored message: right subject, CloudEvents body, and the
	// Nats-Msg-Id = the dedup id (subscription id on the DeliverOne path, which
	// has no delivery row).
	msg, err := stream.GetMsg(ctx, 1)
	if err != nil {
		t.Fatalf("get stream msg: %v", err)
	}
	if msg.Subject != subject {
		t.Errorf("stored subject = %q, want %q", msg.Subject, subject)
	}
	if gotID := msg.Header.Get(nats.MsgIdHdr); gotID != sub.SubscriptionID.String() {
		t.Errorf("Nats-Msg-Id = %q, want %q (the CloudEvents id / dedup key)", gotID, sub.SubscriptionID.String())
	}
	var env struct {
		SpecVersion string `json:"specversion"`
		Type        string `json:"type"`
		ID          string `json:"id"`
	}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("stored body not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.object.uploaded" || env.ID != sub.SubscriptionID.String() {
		t.Errorf("envelope mismatch: %+v", env)
	}

	// Redeliver the SAME event (same subscription → same Nats-Msg-Id): JetStream
	// dedups within the window, so the stream still holds exactly one message.
	if err := d.DeliverOne(ctx, sub, "paladin.object.uploaded"); err != nil {
		t.Fatalf("DeliverOne (redeliver): %v", err)
	}
	info2, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info 2: %v", err)
	}
	if info2.State.Msgs != 1 {
		t.Errorf("stream has %d messages after redeliver, want 1 (JetStream must dedup on Nats-Msg-Id)", info2.State.Msgs)
	}
}

// seedJetStreamSubscription inserts a jetstream-mode NATS subscription.
func (f *dispatcherFixture) seedJetStreamSubscription(t *testing.T, tenant uuid.UUID, url, subject string) {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"url": url, "subject": subject, "jetstream": true})
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_subscriptions
		   (id, tenant_id, cel_filter, sink_kind, sink_config, disabled)
		 VALUES ($1, $2, '', 'nats', $3, false)`,
		uuid.New(), tenant, cfg,
	); err != nil {
		t.Fatalf("seed jetstream sub: %v", err)
	}
}

// jetStreamOutbox queues one event for a jetstream subscription on subject and
// runs one outbox tick, returning the delivery row it produced.
func jetStreamOutbox(t *testing.T, url, subject string) (*dispatcherFixture, deliveryRow) {
	t.Helper()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-jetstream")
	f.seedJetStreamSubscription(t, tenant, url, subject)

	pool := worker.NewNatsConnPool(nil)
	t.Cleanup(pool.Close)
	d := f.dispatcher()
	d.NATS = pool
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one", rows)
	}
	if n := f.tickOnce(t, f.outboxRunner(d)); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}
	return f, f.deliveryRow(t, rows[0].ID)
}

// outboxStreamSubjects is the stream the outbox test provisions; outboxSubject
// falls under it and unstreamedSubject does not.
const (
	outboxStreamName     = "PALADIN_OUTBOX"
	outboxStreamSubjects = "paladin.outbox.>"
	outboxSubject        = "paladin.outbox.test"
	unstreamedSubject    = "paladin.unstreamed.test"
)

// The outbox runner batches NATS rows by connection, and the batch published
// with core NATS whatever the subscription said: a jetstream subscription got
// neither the stream's acknowledgement nor its Nats-Msg-Id dedup. These pin
// the outbox path, which TestDispatcher_JetStreamSink_PublishesAndDedups
// (DeliverOne) never takes.
func TestDispatcher_JetStreamSinkThroughTheOutbox(t *testing.T) {
	t.Parallel()

	t.Run("lands on the stream under the row's message id", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		url := runEmbeddedJetStream(t)
		conn, err := nats.Connect(url, nats.Timeout(2*time.Second))
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close()
		js, err := jetstream.New(conn)
		if err != nil {
			t.Fatalf("jetstream: %v", err)
		}
		stream, err := js.CreateStream(ctx, jetstream.StreamConfig{
			Name: outboxStreamName, Subjects: []string{outboxStreamSubjects},
		})
		if err != nil {
			t.Fatalf("create stream: %v", err)
		}

		_, row := jetStreamOutbox(t, url, outboxSubject)
		if row.Status != "delivered" {
			t.Fatalf("status = %q, want delivered (last_error=%q)", row.Status, row.LastError)
		}
		msg, err := stream.GetMsg(ctx, 1)
		if err != nil {
			t.Fatalf("get stream msg: %v", err)
		}
		if got := msg.Header.Get(nats.MsgIdHdr); got != row.ID.String() {
			t.Errorf("Nats-Msg-Id = %q, want the delivery row id %s — published "+
				"without JetStream, so the stream cannot deduplicate a retry", got, row.ID)
		}
	})

	t.Run("is not delivered when no stream takes the subject", func(t *testing.T) {
		t.Parallel()
		url := runEmbeddedJetStream(t)

		_, row := jetStreamOutbox(t, url, unstreamedSubject)
		if row.Status == "delivered" {
			t.Errorf("a jetstream row nothing stored was marked delivered — " +
				"the core publish had no acknowledgement to fail on")
		}
		if row.LastError == "" {
			t.Error("the failed publish recorded no last_error")
		}
	})
}
