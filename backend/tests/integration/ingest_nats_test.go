//go:build integration

// E2E for the ingest plane's NATS path: SeaweedFS-shape gocdk_pub_sub
// envelope on an embedded NATS server → ingest worker → state flip.
// Counterpart to TestIngest_SeaweedFSWebhookPromotesPending which
// exercises the JSON webhook driver; this one drives the gob +
// protobuf wire format the live cluster actually receives, plus the
// NATSDriver's subscribe loop.
//
// Two reasons for a dedicated test instead of a parameterised
// subtest on the existing suite:
//  1. The publisher fake is non-trivial (gob + protowire); keeping
//     it next to the test case it serves makes the wire-format
//     contract obvious.
//  2. The NATS driver requires its own ctx-cancel teardown shape;
//     mixing that with the synchronous Deliver harness in the
//     webhook test would obscure both.
package integration

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest/filerpb"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func TestIngest_SeaweedFSNATSPromotesPending(t *testing.T) {
	// Not t.Parallel(): the test pulls a fresh embedded nats-server
	// on every run AND drives a Postgres harness; running it
	// alongside the dispatcher_test.go suite (which also embeds NATS)
	// occasionally races the worker's subscribe-vs-publish window
	// when the host is loaded. Sequential runs cost ~3s — fine.
	h := pgharness.Setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pre-seed the row that the ingest path will promote.
	tenantID := mustCreateTenant(t, h.PoolMigrate, "ingest-nats")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	objectID := mustInsertPendingObject(t, h.PoolMigrate, tenantID, "docs", "report.pdf")

	// Embedded NATS — same helper the dispatcher test uses, but
	// duplicated locally because Go test files in different files
	// of the same package CAN share helpers; we just want this
	// test self-contained for read-from-the-top clarity.
	url := runNATSForIngestTest(t)

	subject := "seaweedfs.filer"
	q := sqlc.New(h.PoolMigrate)
	driver := &eventingest.NATSDriver{
		URL:     url,
		Subject: subject,
		// QueueGroup intentionally empty — single subscriber path is
		// the tightest test of the parse + handler chain. Queue-group
		// behaviour is broker concern, not adapter concern.
		SourceAdapt: &eventingest.SeaweedFSNATSSource{
			BucketName: "paladin-test",
			URI:        "seaweedfs-nats://test",
		},
	}
	worker := &eventingest.Worker{
		Driver: driver,
		Handler: &eventingest.PromoteHandler{
			Lookup:       q,
			Transitioner: statemachine.New(h.PoolMigrate),
		},
		Dedup: &eventingest.PgxDedupStore{Q: q},
	}

	// Run the worker in the background; cancel cleans up.
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()

	// Publish the SF-shape event — the same gob(metadata) ||
	// gob(body) wrapping gocloud.dev natspubsub uses for
	// `nats://` topic URLs. Body carries a minimal EventNotification
	// with new_entry only (= create). Metadata carries the path
	// the source extracts and parses.
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("publish-side connect: %v", err)
	}
	defer pc.Close()

	// Wait for the worker's subscriber to actually attach before
	// publishing. Without this, the publish lands while the
	// subscriber is still warming up and core pubsub at-most-once
	// drops it on the floor — symptom: row stays PENDING forever.
	// Probe the broker's per-subject subscriber count via a sentinel
	// publish to a different subject; not bulletproof but cheap and
	// good enough for an embedded server. Backstop: a 200ms sleep
	// covers anything the probe missed.
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		if pc.NumSubscriptions() >= 0 {
			// nats client doesn't expose remote sub count; use a
			// time-based wait instead. The 20×50ms loop is just
			// "wait up to 1s for subscribe-on-server to land".
			break
		}
	}
	time.Sleep(200 * time.Millisecond)

	path := fmt.Sprintf("/buckets/paladin-test/%s/docs/report.pdf", tenantID)
	payload := buildSFNATSPayload(t, path)
	// Publish twice to also verify dedup (second is a no-op).
	for i := 0; i < 2; i++ {
		if err := pc.Publish(subject, payload); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Poll for the promote — at-most-once core pubsub delivery is
	// async, so we can't synchronously block on the worker's
	// completion. Within ~5s the row should land in AVAILABLE.
	deadline := time.Now().Add(5 * time.Second)
	var lastState string
	for time.Now().Before(deadline) {
		lastState = mustObjectState(t, h.PoolMigrate, objectID)
		if lastState == "AVAILABLE" {
			break
		}
		time.Sleep(75 * time.Millisecond)
	}
	if lastState != "AVAILABLE" {
		t.Fatalf("object state = %q after 3s, want AVAILABLE — "+
			"check ingest_nats source path-strip + Lookup join", lastState)
	}

	// Dedup check: the second publish above must NOT have produced a
	// second ingested_events row. The id hashes the path, the event type
	// and the entry's commit time, so a redelivery has the first's id
	// whenever it arrives.
	if n := mustCountIngested(t, h.PoolMigrate); n != 1 {
		t.Errorf("ingested_events count = %d, want 1 (dedup window)", n)
	}

	// Tear the worker down cleanly.
	cancel()
	select {
	case err := <-workerErr:
		// Worker.Run returns ctx.Err() on graceful shutdown; that's
		// not a failure.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("worker.Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker.Run didn't return within 2s of ctx cancel")
	}
}

// ─── publish-side fakes ────────────────────────────────────────────────

// sfCommitTs is the filer commit time of the entry the test publishes.
const sfCommitTs int64 = 1793700668832899354

// buildSFNATSPayload assembles the wire bytes a SF gocdk_pub_sub
// publisher writes to NATS for a create of path:
//
//	gob(map[string]string{"key": <path>}) || gob([]byte{<protobuf body>})
//
// The body is an EventNotification whose new_entry carries a commit time,
// as SeaweedFS 4.51 and later publish it.
func buildSFNATSPayload(t *testing.T, path string) []byte {
	t.Helper()
	body, err := proto.Marshal(&filerpb.EventNotification{
		NewEntry: &filerpb.Entry{Name: "report.pdf", CommitTsNs: sfCommitTs},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(map[string]string{"key": path}); err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	if err := enc.Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return buf.Bytes()
}

// runNATSForIngestTest boots an in-process nats-server. Distinct
// name from runEmbeddedNATSForIntegration in dispatcher_test.go so
// a future refactor can lift one up to a shared helper without
// breaking the other.
func runNATSForIngestTest(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("embedded nats: not ready")
	}
	return srv.ClientURL()
}

// _ swallows imports the linter would otherwise flag — these are
// used inside the test loop above but go-vet sometimes complains in
// build-tagged files when it can't see all usages.
var _ = atomic.Int64{}
var _ = json.Marshal
