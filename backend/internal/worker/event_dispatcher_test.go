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

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

type fakeStore struct {
	subs []admindomain.EventSubscription
}

func (f *fakeStore) List(_ context.Context, _ admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return f.subs, "", nil
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
		Type:     "object.created",
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
	if rows[0].EventType != "object.created" {
		t.Errorf("event type: got %q", rows[0].EventType)
	}
	if len(rows[0].EventPayload) == 0 {
		t.Error("event payload is empty")
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
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "object.created"})
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

func TestDispatchFilterMatch(t *testing.T) {
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
				CELFilter:      "object.deleted", // exact-match in v2
			}},
		},
		Outbox: out,
	}
	n, _ := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "object.created"})
	if n != 0 {
		t.Errorf("queued: got %d want 0 (filter mismatch)", n)
	}
	if got := len(out.snapshot()); got != 0 {
		t.Errorf("outbox writes: got %d want 0", got)
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
