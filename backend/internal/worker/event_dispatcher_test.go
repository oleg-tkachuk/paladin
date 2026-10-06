package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	jwt "github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

type fakeStore struct {
	subs []admindomain.EventSubscription
	// lastListArgs records the most recent List call so tests can
	// assert the producer scopes the query (tenant filter) instead of
	// fetching everything and filtering in-process.
	lastListArgs admindomain.ListEventSubscriptionsArgs
}

func (f *fakeStore) List(_ context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	f.lastListArgs = args
	if args.TenantID == uuid.Nil {
		return f.subs, "", nil
	}
	var out []admindomain.EventSubscription
	for _, s := range f.subs {
		if s.TenantID == args.TenantID {
			out = append(out, s)
		}
	}
	return out, "", nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	for _, s := range f.subs {
		if s.SubscriptionID == id {
			return s, nil
		}
	}
	return admindomain.EventSubscription{}, errors.New("not found")
}

// fakeOutbox captures what the producer writes so tests can assert
// on row shape without standing up Postgres.
type fakeOutbox struct {
	mu   sync.Mutex
	rows []OutboxRow
}

func (f *fakeOutbox) Insert(_ context.Context, row OutboxRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeOutbox) snapshot() []OutboxRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]OutboxRow, len(f.rows))
	copy(out, f.rows)
	return out
}

// TestDispatchWritesOutbox replaces the pre-slice-9 "Dispatch POSTs
// synchronously" test. The producer no longer touches the network —
// it writes one row per matching sub to the outbox and returns.
func TestDispatchWritesOutbox(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	subID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{
		"url":                "http://nowhere.invalid",
		"signing_secret_ref": "test-secret",
	})
	out := &fakeOutbox{}
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: subID,
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
			}},
		},
		Outbox: out,
	}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{
		Type:     "paladin.object.uploaded",
		At:       time.Now().UTC(),
		TenantID: tenantID.String(),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if n != 1 {
		t.Errorf("queued: got %d want 1", n)
	}
	rows := out.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows: got %d want 1", len(rows))
	}
	if rows[0].SubscriptionID != subID {
		t.Errorf("sub id: got %s want %s", rows[0].SubscriptionID, subID)
	}
	if rows[0].EventType != "paladin.object.uploaded" {
		t.Errorf("event type: got %q", rows[0].EventType)
	}
	if len(rows[0].EventPayload) == 0 {
		t.Error("event payload is empty")
	}
}

// TestDispatchScopesListToTenant pins the fan-out query shape: the
// producer must push the tenant filter into the store query (SQL
// WHERE) rather than listing every tenant's subscriptions and
// filtering in-process — at scale the unscoped form both leaks work
// across tenants and silently truncates past the page cap.
func TestDispatchScopesListToTenant(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	otherTenant := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})
	store := &fakeStore{
		subs: []admindomain.EventSubscription{
			{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
			},
			{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       otherTenant,
				SinkKind:       "http",
				SinkConfig:     cfg,
			},
		},
	}
	out := &fakeOutbox{}
	d := &Dispatcher{Store: store, Outbox: out}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "paladin.object.uploaded"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if store.lastListArgs.TenantID != tenantID {
		t.Errorf("List args.TenantID: got %s want %s (tenant filter must be pushed into the query)",
			store.lastListArgs.TenantID, tenantID)
	}
	if n != 1 {
		t.Errorf("queued: got %d want 1 (only this tenant's sub)", n)
	}
	if got := len(out.snapshot()); got != 1 {
		t.Errorf("outbox writes: got %d want 1", got)
	}
}

func TestKindFromType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"paladin.object.uploaded", "object"},
		{"paladin.object.deleted", "object"},
		{"paladin.collection.created", "collection"},
		{"paladin.bucket.created", "bucket"},
		{"paladin.tenant.trashed", "tenant"},
		{"paladin.capability.charged", "capability"},
		{"paladin.quota.set", "quota"},
		{"paladin.backend.credentials_rotated", "backend"},
		{"paladin.audit.login", "audit"},
		{"object.created", ""},       // legacy 2-segment, non-canonical
		{"paladin.object", ""},       // no verb segment
		{"paladin.", ""},             // empty kind
		{"not.paladin.prefixed", ""}, // wrong prefix
		{"", ""},
	}
	for _, tc := range cases {
		if got := kindFromType(tc.in); got != tc.want {
			t.Errorf("kindFromType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestClassifyEvent pins the severity of every real event type — it doubles as
// the guard that a destructive verb never silently classifies as "info".
func TestClassifyEvent(t *testing.T) {
	cases := []struct {
		name  string
		evt   Event
		label string
		level int64
	}{
		// Routine → info.
		{"object.uploaded", Event{Type: "paladin.object.uploaded"}, "info", 10},
		{"object.updated", Event{Type: "paladin.object.updated"}, "info", 10},
		{"object.restored", Event{Type: "paladin.object.restored"}, "info", 10},
		{"collection.created", Event{Type: "paladin.collection.created"}, "info", 10},
		{"bucket.created", Event{Type: "paladin.bucket.created"}, "info", 10},
		{"tenant.created", Event{Type: "paladin.tenant.created"}, "info", 10},
		{"tenant.restored", Event{Type: "paladin.tenant.restored"}, "info", 10},
		{"capability.charged", Event{Type: "paladin.capability.charged"}, "info", 10},
		{"quota.set", Event{Type: "paladin.quota.set"}, "info", 10},
		{"audit default", Event{Type: "paladin.audit.login"}, "info", 10},
		{"unknown type", Event{Type: "paladin.future.invented"}, "info", 10},
		// Recoverable-destructive / security → warning (verb heuristic + map).
		{"object.deleted (no mode)", Event{Type: "paladin.object.deleted"}, "warning", 30},
		{"object.deleted soft", Event{Type: "paladin.object.deleted", Payload: map[string]any{"mode": "soft"}}, "warning", 30},
		{"collection.deleted", Event{Type: "paladin.collection.deleted"}, "warning", 30},
		{"bucket.deleting", Event{Type: "paladin.bucket.deleting"}, "warning", 30},
		{"bucket.deleted", Event{Type: "paladin.bucket.deleted"}, "warning", 30},
		{"tenant.trashed (soft)", Event{Type: "paladin.tenant.trashed"}, "warning", 30},
		{"credentials_rotated (map)", Event{Type: "paladin.backend.credentials_rotated"}, "warning", 30},
		// Irreversible → critical.
		{"tenant.purged (hard)", Event{Type: "paladin.tenant.purged"}, "critical", 50},
		{"object.deleted permanent (mode)", Event{Type: "paladin.object.deleted", Payload: map[string]any{"mode": "permanent"}}, "critical", 50},
		// Explicit payload override wins, and carries its ordered level.
		{"payload override critical", Event{Type: "paladin.object.uploaded", Payload: map[string]any{"severity": "critical"}}, "critical", 50},
		{"payload override unknown label → level 0", Event{Type: "paladin.object.uploaded", Payload: map[string]any{"severity": "spicy"}}, "spicy", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEvent(tc.evt)
			if got.label != tc.label || got.level != tc.level {
				t.Errorf("classifyEvent = {%q, %d}, want {%q, %d}", got.label, got.level, tc.label, tc.level)
			}
		})
	}
}

func TestBucketFromResourceName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"storageBackends/primary/buckets/paladin-primary/tenants/t/collections/inv/objects-by-key/k", "paladin-primary"},
		{"storageBackends/primary/buckets/paladin-primary", "paladin-primary"}, // bucket-lifecycle event
		{"tenants/t/collections/inv/objects-by-key/k", ""},                     // C-shape: no bucket
		{"tenants/019f26db-31d0-71ec-9b25-4b3f8636791a", ""},                   // tenant event
		{"", ""},
	}
	for _, tc := range cases {
		if got := bucketFromResourceName(tc.name); got != tc.want {
			t.Errorf("bucketFromResourceName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// pagingStore returns subscriptions in fixed-size pages with a subscription-id
// cursor, like the real repo — so the fan-out's paging loop is exercised. subs
// must be pre-sorted by SubscriptionID (uuid v7 = creation order).
type pagingStore struct {
	subs     []admindomain.EventSubscription
	pageSize int
	calls    int
}

func (s *pagingStore) List(_ context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	s.calls++
	var eligible []admindomain.EventSubscription
	for _, sub := range s.subs {
		if sub.TenantID != args.TenantID {
			continue
		}
		if args.AfterID != uuid.Nil && sub.SubscriptionID.String() <= args.AfterID.String() {
			continue // keyset cursor (v7 ids sort lexicographically by creation time)
		}
		eligible = append(eligible, sub)
	}
	ps := s.pageSize
	if ps <= 0 || ps > len(eligible) {
		ps = len(eligible)
	}
	page := eligible[:ps]
	var next string
	if ps < len(eligible) && ps > 0 {
		next = page[ps-1].SubscriptionID.String()
	}
	return page, next, nil
}

func (s *pagingStore) Get(_ context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	for _, sub := range s.subs {
		if sub.SubscriptionID == id {
			return sub, nil
		}
	}
	return admindomain.EventSubscription{}, errors.New("not found")
}

// TestDispatch_PagesAllSubscriptions: the fan-out must reach EVERY matching
// subscription across pages — the old single-page cap silently dropped subs
// beyond the page size.
func TestDispatch_PagesAllSubscriptions(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})
	const n = 5
	subs := make([]admindomain.EventSubscription, n)
	for i := range subs {
		subs[i] = admindomain.EventSubscription{
			SubscriptionID: uuid.Must(uuid.NewV7()),
			TenantID:       tenantID,
			SinkKind:       "http",
			SinkConfig:     cfg, // empty filter → matches every event
		}
	}
	store := &pagingStore{subs: subs, pageSize: 2}
	out := &fakeOutbox{}
	d := &Dispatcher{Store: store, Outbox: out}

	queued, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "paladin.object.uploaded"})
	if err != nil {
		t.Fatal(err)
	}
	if queued != n {
		t.Fatalf("queued = %d, want %d (every subscription across pages)", queued, n)
	}
	if len(out.snapshot()) != n {
		t.Fatalf("outbox rows = %d, want %d", len(out.snapshot()), n)
	}
	if store.calls < 3 { // 2+2+1 → 3 pages
		t.Errorf("List calls = %d, want >=3 — pagination did not actually page", store.calls)
	}
}

// TestDispatchRejectsInvalidTenantID — Dispatch parses the tenant id
// for the SQL filter; garbage must fail loudly, not fan out to
// nothing.
func TestDispatchRejectsInvalidTenantID(t *testing.T) {
	d := &Dispatcher{Store: &fakeStore{}, Outbox: &fakeOutbox{}}
	if _, err := d.Dispatch(context.Background(), "not-a-uuid", Event{Type: "paladin.object.uploaded"}); err == nil {
		t.Fatal("expected error for invalid tenant id")
	}
}

func TestDispatchSkipsDisabled(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})
	out := &fakeOutbox{}
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
				Disabled:       true,
			}},
		},
		Outbox: out,
	}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "paladin.object.uploaded"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("queued: got %d want 0 (disabled)", n)
	}
	if got := len(out.snapshot()); got != 0 {
		t.Errorf("outbox writes: got %d want 0", got)
	}
}

// TestDispatchFilterMatch exercises the subscription CEL filter
// (EventEnvelopeSchema) at fan-out: matching filters queue, non-matching
// ones drop, richer predicates over envelope fields work, and an
// uncompilable filter fails closed rather than fanning out.
func TestDispatchFilterMatch(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})

	newDispatcher := func(filter string) (*Dispatcher, *fakeOutbox) {
		out := &fakeOutbox{}
		return &Dispatcher{
			Store: &fakeStore{
				subs: []admindomain.EventSubscription{{
					SubscriptionID: uuid.Must(uuid.NewV7()),
					TenantID:       tenantID,
					SinkKind:       "http",
					SinkConfig:     cfg,
					CELFilter:      filter,
				}},
			},
			Outbox: out,
		}, out
	}

	cases := []struct {
		name   string
		filter string
		evt    Event
		want   int
	}{
		{"non-matching type drops", `type == "paladin.object.deleted"`, Event{Type: "paladin.object.uploaded"}, 0},
		{"matching type queues", `type == "paladin.object.uploaded"`, Event{Type: "paladin.object.uploaded"}, 1},
		{"empty filter matches all", "", Event{Type: "paladin.tenant.created"}, 1},
		{"kind derived from type matches", `kind == "object"`, Event{Type: "paladin.object.uploaded"}, 1},
		{"kind derived (collection) matches", `kind == "collection"`, Event{Type: "paladin.collection.created"}, 1},
		{"kind mismatch drops", `kind == "bucket"`, Event{Type: "paladin.object.uploaded"}, 0},
		{
			"predicate over envelope field",
			`type == "paladin.object.uploaded" && actor_subject == "svc"`,
			Event{Type: "paladin.object.uploaded", ActorSubject: "svc"},
			1,
		},
		{
			"payload-derived collection matches",
			`collection == "invoices"`,
			Event{Type: "paladin.object.uploaded", Payload: map[string]any{"collection": "invoices"}},
			1,
		},
		{
			"payload-derived size_bytes predicate",
			`size_bytes > 1000`,
			Event{Type: "paladin.object.uploaded", Payload: map[string]any{"size_bytes": int64(2048)}},
			1,
		},
		{
			"payload-derived field absent → false, not eval error",
			`severity == "high"`,
			Event{Type: "paladin.object.uploaded"}, // no Payload
			0,
		},
		{
			"bucket_name derived from A-shape resource_name",
			`bucket_name == "paladin-primary"`,
			Event{
				Type:         "paladin.object.uploaded",
				ResourceName: "storageBackends/primary/buckets/paladin-primary/tenants/t/collections/inv/objects-by-key/k",
			},
			1,
		},
		{
			"C-shape resource_name → bucket_name empty → no match",
			`bucket_name == "paladin-primary"`,
			Event{Type: "paladin.object.uploaded", ResourceName: "tenants/t/collections/inv/objects-by-key/k"},
			0,
		},
		{"severity == critical matches a hard tenant delete", `severity == "critical"`, Event{Type: "paladin.tenant.purged"}, 1},
		{"severity_level threshold admits warning", `severity_level >= 30`, Event{Type: "paladin.object.deleted"}, 1},
		{"severity_level threshold drops info", `severity_level >= 30`, Event{Type: "paladin.object.uploaded"}, 0},
		{
			"object hard delete (mode) escalates to critical",
			`severity == "critical"`,
			Event{Type: "paladin.object.deleted", Payload: map[string]any{"mode": "permanent"}},
			1,
		},
		{
			"object soft delete stays below critical",
			`severity == "critical"`,
			Event{Type: "paladin.object.deleted", Payload: map[string]any{"mode": "soft"}},
			0,
		},
		// A pre-validation legacy value (bare event type, not a bool CEL
		// expression) can't compile → fail-closed, not fan-out.
		{"uncompilable filter fails closed", "paladin.object.uploaded", Event{Type: "paladin.object.uploaded"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, out := newDispatcher(tc.filter)
			n, err := d.Dispatch(context.Background(), tenantID.String(), tc.evt)
			if err != nil {
				t.Fatal(err)
			}
			if n != tc.want || len(out.snapshot()) != tc.want {
				t.Errorf("queued=%d writes=%d, want %d/%d", n, len(out.snapshot()), tc.want, tc.want)
			}
		})
	}
}

// TestDeliverOneSucceeds keeps the synchronous "Test Webhook" path
// honest — operators clicked Test, they want a HTTP round-trip and
// a result, no outbox row written.
func TestDeliverOneSucceeds(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(map[string]any{
		"url":                srv.URL,
		"signing_secret_ref": "test-secret",
	})
	d := &Dispatcher{
		HTTPClient:  srv.Client(),
		MaxAttempts: 1,
	}
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       tenantID,
		SinkKind:       "http",
		SinkConfig:     cfg,
	}
	if err := d.DeliverOne(context.Background(), sub, "paladin.test"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if len(bodies) != 1 {
		t.Errorf("bodies: got %d want 1", len(bodies))
	}
}

// runEmbeddedNATS spins up an in-process nats-server on a random port
// and returns its client URL. The server is stopped on test cleanup.
// Used by the NATS sink tests so we don't depend on external infra.
func runEmbeddedNATS(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // pick a free port
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("embedded nats: not ready")
	}
	_ = natsserver.Options{} // keep import alive even when fields not referenced
	return srv.ClientURL()
}

// TestDispatcher_NATSDelivery boots an in-process NATS server,
// subscribes a probe consumer to the configured subject, drives one
// DeliverOne, and asserts the CloudEvents envelope landed on the wire.
func TestDispatcher_NATSDelivery(t *testing.T) {
	url := runEmbeddedNATS(t)

	// Probe consumer.
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	defer pc.Close()
	got := make(chan *nats.Msg, 1)
	if _, err := pc.Subscribe("paladin.events.>", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush sub: %v", err)
	}

	tenantID := uuid.Must(uuid.NewV7())
	subID := uuid.Must(uuid.NewV7())
	subject := "paladin.events." + tenantID.String() + ".object.uploaded"
	cfg, _ := json.Marshal(map[string]any{
		"url":     url,
		"subject": subject,
	})
	pool := NewNatsConnPool(nil)
	defer pool.Close()
	d := &Dispatcher{NATS: pool, MaxAttempts: 1}
	sub := admindomain.EventSubscription{
		SubscriptionID: subID,
		TenantID:       tenantID,
		SinkKind:       "nats",
		SinkConfig:     cfg,
	}
	if err := d.DeliverOne(context.Background(), sub, "paladin.object.uploaded"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}

	select {
	case m := <-got:
		if m.Subject != subject {
			t.Errorf("subject: got %q want %q", m.Subject, subject)
		}
		var env struct {
			SpecVersion string `json:"specversion"`
			Type        string `json:"type"`
			TenantID    string `json:"tenantid"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.SpecVersion != "1.0" {
			t.Errorf("specversion: got %q want 1.0", env.SpecVersion)
		}
		if env.Type != "paladin.object.uploaded" {
			t.Errorf("type: got %q", env.Type)
		}
		if env.TenantID != tenantID.String() {
			t.Errorf("tenantid: got %q want %s", env.TenantID, tenantID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nats: no message received within 2s")
	}

	// Pool reuses the conn — second DeliverOne shouldn't dial again.
	if err := d.DeliverOne(context.Background(), sub, "paladin.object.uploaded"); err != nil {
		t.Fatalf("DeliverOne (2): %v", err)
	}
	if got := pool.Statuses(); len(got) != 1 {
		t.Errorf("pool size: got %d want 1 (connection reuse)", len(got))
	}
}

// TestDispatcher_NATSEventID asserts the CloudEvents `id` is the per-event
// delivery id the drain loop stamps (Event.ID) — unique per event so
// consumers can dedup — and validates the wider envelope (source, data).
// The drain-loop stamping is simulated by setting Event.ID directly.
func TestDispatcher_NATSEventID(t *testing.T) {
	url := runEmbeddedNATS(t)

	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	defer pc.Close()
	msgs := make(chan *nats.Msg, 2)
	if _, err := pc.Subscribe("paladin.events.>", func(m *nats.Msg) { msgs <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": url, "subject": "paladin.events.x"})
	pool := NewNatsConnPool(nil)
	defer pool.Close()
	d := &Dispatcher{NATS: pool}
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       tenantID,
		SinkKind:       "nats",
		SinkConfig:     cfg,
	}

	id1, id2 := uuid.NewString(), uuid.NewString()
	for _, id := range []string{id1, id2} {
		if _, err := d.deliverNATS(context.Background(), sub, Event{
			Type:         "paladin.object.uploaded",
			At:           time.Now().UTC(),
			TenantID:     tenantID.String(),
			ResourceName: "r",
			Payload:      map[string]any{"k": "v"},
			ID:           id, // drain loop stamps the event_deliveries row id
		}); err != nil {
			t.Fatalf("deliverNATS: %v", err)
		}
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case m := <-msgs:
			var env cloudEventEnvelope
			if err := json.Unmarshal(m.Data, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if env.SpecVersion != "1.0" {
				t.Errorf("specversion: %q", env.SpecVersion)
			}
			if env.Source == "" {
				t.Error("envelope source is empty")
			}
			if env.Data["k"] != "v" {
				t.Errorf("data: %v", env.Data)
			}
			seen[env.ID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("nats: missing message")
		}
	}
	if !seen[id1] || !seen[id2] {
		t.Errorf("CloudEvents id was not the stamped delivery id; saw %v", seen)
	}
	if len(seen) != 2 {
		t.Errorf("CloudEvents id not unique per event; saw %v", seen)
	}
}

// TestDispatcher_NATSMissingConfig: malformed sink config / empty URL
// surfaces as a delivery error with status=0 (matches HTTP transport
// error semantics on the row).
func TestDispatcher_NATSMissingConfig(t *testing.T) {
	pool := NewNatsConnPool(nil)
	defer pool.Close()
	d := &Dispatcher{NATS: pool}
	cfg, _ := json.Marshal(map[string]any{"subject": "paladin.events.x"}) // url missing
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       "nats",
		SinkConfig:     cfg,
	}
	if err := d.DeliverOne(context.Background(), sub, "paladin.test"); err == nil {
		t.Fatal("expected error for missing url, got nil")
	}
}

// TestParseNatsCredentials_Schemes locks the credential-ref contract: token,
// nkey and jwt schemes parse, malformed or half-empty forms and unknown
// schemes reject.
func TestParseNatsCredentials_Schemes(t *testing.T) {
	cases := []struct {
		ref     string
		wantErr bool
		wantNil bool
	}{
		{"", false, true},
		{"token:abc", false, false},
		{"token:", true, false},
		{"nkey:SUACS", true, false},  // malformed seed → error
		{"jwt:onlyjwt", true, false}, // missing '+<seed>' half → error
		{"unknown:x", true, false},
		{"noscheme", true, false},
		// The half-empty forms. NatsConnPool.get dials only after this
		// returns, so any of these coming back as a nil option with no error
		// would connect ANONYMOUSLY to a cluster the operator configured
		// credentials for.
		{":s3cret", true, false},   // empty scheme
		{"nkey:", true, false},     // scheme with no seed
		{"jwt:+seed", true, false}, // empty jwt half
		{"jwt:abc+", true, false},  // empty seed half
	}
	for _, c := range cases {
		opt, err := parseNatsCredentials(c.ref)
		if (err != nil) != c.wantErr {
			t.Errorf("ref=%q err=%v wantErr=%v", c.ref, err, c.wantErr)
		}
		if c.wantNil && opt != nil {
			t.Errorf("ref=%q: expected nil option, got %v", c.ref, opt)
		}
		// An error must never arrive alongside a usable option: get() checks
		// the error, but a caller that looked at the option first would dial
		// with a credential the parser rejected.
		if c.wantErr && opt != nil {
			t.Errorf("ref=%q: rejected, yet returned a usable option", c.ref)
		}
	}

	// Positive: a real user nkey seed and a jwt+seed both parse to a
	// non-nil option without error.
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	seed, _ := ukp.Seed()
	if opt, err := parseNatsCredentials("nkey:" + string(seed)); err != nil || opt == nil {
		t.Errorf("valid nkey seed: opt=%v err=%v", opt, err)
	}
	if opt, err := parseNatsCredentials("jwt:eyJ0eXAi.eyJzdWIi+" + string(seed)); err != nil || opt == nil {
		t.Errorf("valid jwt+seed: opt=%v err=%v", opt, err)
	}
	// The `nkey:` prefix on the seed half is tolerated.
	if opt, err := parseNatsCredentials("jwt:eyJ0eXAi.eyJzdWIi+nkey:" + string(seed)); err != nil || opt == nil {
		t.Errorf("valid jwt+nkey:seed: opt=%v err=%v", opt, err)
	}
}

// natsAuthRoundTrip drives one DeliverOne against an auth-enabled NATS
// server (the probe consumer authenticates with the same credentials_ref)
// and asserts the CloudEvents envelope landed — proving the credential
// scheme completes the server's auth handshake.
func natsAuthRoundTrip(t *testing.T, url, credsRef string) {
	t.Helper()
	authOpt, err := parseNatsCredentials(credsRef)
	if err != nil {
		t.Fatalf("parse creds: %v", err)
	}
	pc, err := nats.Connect(url, authOpt, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect (auth): %v", err)
	}
	defer pc.Close()
	got := make(chan *nats.Msg, 1)
	if _, err := pc.Subscribe("paladin.events.>", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush sub: %v", err)
	}

	tenantID := uuid.Must(uuid.NewV7())
	subject := "paladin.events." + tenantID.String() + ".object.uploaded"
	cfg, _ := json.Marshal(map[string]any{"url": url, "subject": subject, "credentials_ref": credsRef})
	pool := NewNatsConnPool(nil)
	defer pool.Close()
	d := &Dispatcher{NATS: pool, MaxAttempts: 1}
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       tenantID,
		SinkKind:       "nats",
		SinkConfig:     cfg,
	}
	if err := d.DeliverOne(context.Background(), sub, "paladin.object.uploaded"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	select {
	case m := <-got:
		if m.Subject != subject {
			t.Errorf("subject: got %q want %q", m.Subject, subject)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nats: no message received within 2s — auth round-trip failed")
	}
}

// TestDispatcher_NATSDelivery_NKeyAuth proves the nkey: scheme completes
// the NKey challenge-response against an auth-enabled embedded server.
func TestDispatcher_NATSDelivery_NKeyAuth(t *testing.T) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	pub, _ := ukp.PublicKey()
	seed, _ := ukp.Seed()

	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.Nkeys = []*natsserver.NkeyUser{{Nkey: pub}}
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(2 * time.Second) {
		t.Fatal("embedded nats (nkey): not ready")
	}
	url := srv.ClientURL()

	// Auth is genuinely enforced — an anonymous connect must be rejected.
	if c, err := nats.Connect(url, nats.Timeout(time.Second)); err == nil {
		c.Close()
		t.Fatal("anonymous connect should be rejected when nkey auth is on")
	}

	natsAuthRoundTrip(t, url, "nkey:"+string(seed))
}

// TestDispatcher_NATSDelivery_JWTAuth proves the jwt: scheme completes
// decentralized (operator/account/user JWT) auth against an embedded
// server running a trusted operator + in-memory account resolver.
func TestDispatcher_NATSDelivery_JWTAuth(t *testing.T) {
	okp, _ := nkeys.CreateOperator()
	opub, _ := okp.PublicKey()

	// System account — operator mode expects one.
	skp, _ := nkeys.CreateAccount()
	spub, _ := skp.PublicKey()
	sjwt, err := jwt.NewAccountClaims(spub).Encode(okp)
	if err != nil {
		t.Fatalf("sys account encode: %v", err)
	}

	oc := jwt.NewOperatorClaims(opub)
	oc.Name = "TESTOP"
	oc.SystemAccount = spub
	ojwt, err := oc.Encode(okp)
	if err != nil {
		t.Fatalf("operator encode: %v", err)
	}
	opClaims, err := jwt.DecodeOperatorClaims(ojwt)
	if err != nil {
		t.Fatalf("operator decode: %v", err)
	}

	// Workload account + user (user JWT signed by the account).
	akp, _ := nkeys.CreateAccount()
	apub, _ := akp.PublicKey()
	ajwt, err := jwt.NewAccountClaims(apub).Encode(okp)
	if err != nil {
		t.Fatalf("account encode: %v", err)
	}
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	useed, _ := ukp.Seed()
	ujwt, err := jwt.NewUserClaims(upub).Encode(akp)
	if err != nil {
		t.Fatalf("user encode: %v", err)
	}

	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.TrustedOperators = []*jwt.OperatorClaims{opClaims}
	opts.SystemAccount = spub
	res := &natsserver.MemAccResolver{}
	if err := res.Store(spub, sjwt); err != nil {
		t.Fatalf("resolver store sys: %v", err)
	}
	if err := res.Store(apub, ajwt); err != nil {
		t.Fatalf("resolver store acc: %v", err)
	}
	opts.AccountResolver = res
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(2 * time.Second) {
		t.Fatal("embedded nats (jwt): not ready")
	}

	natsAuthRoundTrip(t, srv.ClientURL(), "jwt:"+ujwt+"+"+string(useed))
}

// TestOutboxRunnerBackoff exercises the backoff curve without standing
// up Postgres — backoffFor is a pure function and the table-driven
// shape lets a future tweak land with a single case-row.
func TestOutboxRunnerBackoff(t *testing.T) {
	r := &OutboxRunner{
		BaseBackoff: 5 * time.Second,
		MaxBackoff:  1 * time.Hour,
	}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{5, 80 * time.Second},
		{20, 1 * time.Hour}, // capped
	}
	for _, c := range cases {
		got := r.backoffFor(c.attempt)
		if got != c.want {
			t.Errorf("attempt=%d backoff=%v want=%v", c.attempt, got, c.want)
		}
	}
}

// TestOutboxRunnerMaxAttempts exercises the per-sub override —
// HttpSink.MaxAttempts wins when set, runner default fills in zero.
func TestOutboxRunnerMaxAttempts(t *testing.T) {
	r := &OutboxRunner{DefaultMaxAttempts: 5}
	subWithOverride, _ := json.Marshal(map[string]any{"max_attempts": 9})
	subDefault, _ := json.Marshal(map[string]any{})

	cases := []struct {
		name string
		sub  admindomain.EventSubscription
		want int
	}{
		{"override", admindomain.EventSubscription{SinkKind: "http", SinkConfig: subWithOverride}, 9},
		{"default-when-unset", admindomain.EventSubscription{SinkKind: "http", SinkConfig: subDefault}, 5},
		{"default-for-non-http", admindomain.EventSubscription{SinkKind: "kafka"}, 5},
	}
	for _, c := range cases {
		if got := r.maxAttemptsFor(c.sub); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

// Every worker in this package spells its defaults the same way: a
// configured zero means "unset, use the default". Relaxing one `<= 0` to
// `< 0` lets a real zero through, and a zero is never a usable value for any
// of them — a zero backoff retries instantly and forever, a zero batch claims
// no rows, a zero attempt budget dead-letters on the first try.
//
// That last one is not hypothetical: it is recorded in this package's history
// as a fixed bug, which is why the defaulting is worth holding rather than
// assumed. The existing backoff test sets both fields explicitly and so never
// reaches the default branch.
func TestOutboxRunnerBackoff_DefaultsWhenUnset(t *testing.T) {
	unset := &OutboxRunner{} // BaseBackoff and MaxBackoff both zero
	configured := &OutboxRunner{BaseBackoff: time.Second, MaxBackoff: time.Hour}

	if got := unset.backoffFor(1); got != 5*time.Second {
		t.Errorf("first attempt with no BaseBackoff = %v, want the 5s default — "+
			"a zero base retries with no delay at all", got)
	}
	if got := configured.backoffFor(1); got != time.Second {
		t.Errorf("a configured BaseBackoff was overridden: %v", got)
	}

	// The cap defaults too, and it has to bite before the doubling runs away:
	// 5s * 2^19 is over two weeks.
	if got := unset.backoffFor(20); got != time.Hour {
		t.Errorf("attempt 20 with no MaxBackoff = %v, want the 1h cap", got)
	}

	// Doubling between the two, so the default does not quietly flatten the
	// curve into a constant.
	if a, b := unset.backoffFor(2), unset.backoffFor(3); b != 2*a {
		t.Errorf("backoff %v then %v — the curve is not doubling", a, b)
	}
}
