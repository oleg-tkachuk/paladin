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
