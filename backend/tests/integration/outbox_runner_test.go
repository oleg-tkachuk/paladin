//go:build integration

// The OutboxRunner's own decisions, as opposed to the delivery round-trips
// dispatcher_test.go walks: which failures end a row for good, which leave it
// to be retried, and the defaults a zero-valued runner falls back to.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/sinkkind"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// queueRow writes one pending delivery row straight into the outbox, for the
// states Dispatch will not produce: an undecodable payload, or a row for a
// subscription that was disabled after it was queued.
func (f *dispatcherFixture) queueRow(t *testing.T, tenant, sub uuid.UUID, payload []byte) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload)
		 VALUES ($1, $2, $3, $4, now(), $5)`,
		id, tenant, sub, "paladin.object.uploaded", payload,
	); err != nil {
		t.Fatalf("queue delivery row: %v", err)
	}
	return id
}

func eventPayload(t *testing.T, tenant uuid.UUID) []byte {
	t.Helper()
	raw, err := json.Marshal(makeEvent("", tenant))
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return raw
}

// A payload that does not decode can never be delivered, however often it is
// retried, so the first attempt ends it.
func TestOutboxRunner_UndecodablePayloadFailsAtOnce(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-undecodable")
	sub := f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	// Valid jsonb, but `type` is a number where Event holds a string.
	id := f.queueRow(t, tenant, sub, []byte(`{"type": 123}`))

	f.tickOnce(t, f.outboxRunner(f.dispatcher()))

	row := f.deliveryRow(t, id)
	if row.Status != "failed" || row.Attempts != 1 {
		t.Fatalf("status=%q attempts=%d, want failed/1", row.Status, row.Attempts)
	}
	if !strings.HasPrefix(row.LastError, "decode payload") {
		t.Errorf("last_error = %q, want a decode payload error", row.LastError)
	}
	if rec.count() != 0 {
		t.Errorf("sink saw %d requests for an undecodable row, want 0", rec.count())
	}
}

// A subscription switched off after its row was queued ends the row rather
// than leaving it pending forever.
func TestOutboxRunner_DisabledSubscriptionFailsTheRow(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-disabled")
	sub := f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL, Disabled: true})
	// Dispatch skips a disabled subscription, so queue the row by hand.
	id := f.queueRow(t, tenant, sub, eventPayload(t, tenant))

	f.tickOnce(t, f.outboxRunner(f.dispatcher()))

	row := f.deliveryRow(t, id)
	if row.Status != "failed" || row.Attempts != 1 || row.LastError != "subscription disabled" {
		t.Fatalf("status=%q attempts=%d last_error=%q, want failed/1/subscription disabled",
			row.Status, row.Attempts, row.LastError)
	}
	if rec.count() != 0 {
		t.Errorf("sink saw %d requests for a disabled subscription, want 0", rec.count())
	}
}

// httpOff switches the http sink kind off and leaves the rest on.
type httpOff struct{}

func (httpOff) Enabled(kind string) bool { return kind != sinkkind.HTTP }

// A sink kind switched off in the configuration ends its rows the same way,
// naming the key that turns it back on; nothing reaches the sink.
func TestOutboxRunner_OffSinkKindFailsTheRow(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-kind-off")
	sub := f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	id := f.queueRow(t, tenant, sub, eventPayload(t, tenant))

	d := f.dispatcher()
	d.Sinks = httpOff{}
	f.tickOnce(t, f.outboxRunner(d))

	row := f.deliveryRow(t, id)
	want := "sink kind http is off by configuration: " + config.SinkSwitchKey(sinkkind.HTTP)
	if row.Status != "failed" || row.Attempts != 1 || row.LastError != want {
		t.Fatalf("status=%q attempts=%d last_error=%q, want failed/1/%q",
			row.Status, row.Attempts, row.LastError, want)
	}
	if rec.count() != 0 {
		t.Errorf("sink saw %d requests for a kind switched off, want 0", rec.count())
	}
}

// flakyStore fails every subscription lookup with an error that is not
// ErrNotFound: the database blinked, the subscription is still there.
type flakyStore struct {
	directSubStore
	err error
}

func (s flakyStore) Get(context.Context, uuid.UUID) (admindomain.EventSubscription, error) {
	return admindomain.EventSubscription{}, s.err
}

// A lookup that fails for a reason other than the subscription being gone is
// transient: the row is retried, not discarded.
func TestOutboxRunner_SubscriptionLookupErrorIsRetried(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-lookup")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	d := f.dispatcher()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	id := f.allDeliveryRows(t, tenant)[0].ID

	lookupErr := errors.New("connection reset by peer")
	d.Store = flakyStore{directSubStore: directSubStore{pool: f.h.PoolMigrate}, err: lookupErr}
	f.tickOnce(t, f.outboxRunner(d))

	row := f.deliveryRow(t, id)
	if row.Status != "pending" || row.Attempts != 1 {
		t.Fatalf("status=%q attempts=%d, want pending/1", row.Status, row.Attempts)
	}
	if want := "subscription lookup: " + lookupErr.Error(); row.LastError != want {
		t.Errorf("last_error = %q, want %q", row.LastError, want)
	}
}

// A runner left with a zero BatchSize still claims rows.
func TestOutboxRunner_ZeroBatchSizeUsesTheDefault(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-batch-default")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	d := f.dispatcher()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	r := f.outboxRunner(d)
	r.BatchSize = 0
	if n := f.tickOnce(t, r); n != 1 {
		t.Fatalf("tick processed %d rows, want 1", n)
	}
	if got := f.allDeliveryRows(t, tenant)[0].Status; got != "delivered" {
		t.Errorf("status = %q, want delivered", got)
	}
}

var _ worker.SubscriptionStore = flakyStore{}

// TestOutboxRunner_DeliveryStats pins the operator view: totals over every
// row, one entry per subscription with work left (a subscription whose rows
// were all delivered has nothing to report), and the most recent attempt's
// failure as that entry's reason.
func TestOutboxRunner_DeliveryStats(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	ctx := context.Background()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-stats")
	healthy := f.seedSubscription(t, tenant, subOpts{URL: "http://healthy.invalid"})
	stuck := f.seedSubscription(t, tenant, subOpts{URL: "http://stuck.invalid"})
	payload := eventPayload(t, tenant)

	const (
		olderError = "HTTP 503"
		newerError = "HTTP 500"
		newerCode  = 500
	)
	for _, row := range []struct {
		sub                uuid.UUID
		status, lastError  string
		lastCode           int
		lastAttemptMinsAgo int
	}{
		{healthy, "delivered", "", 200, 5},
		{stuck, "pending", olderError, 503, 10},
		{stuck, "failed", newerError, newerCode, 1},
	} {
		id := f.queueRow(t, tenant, row.sub, payload)
		if _, err := f.h.PoolMigrate.Exec(ctx,
			`UPDATE event_deliveries
			    SET status = $2, last_error = NULLIF($3, ''), last_status_code = $4,
			        last_attempt_at = now() - make_interval(mins => $5)
			  WHERE id = $1`,
			id, row.status, row.lastError, row.lastCode, row.lastAttemptMinsAgo,
		); err != nil {
			t.Fatalf("set row state: %v", err)
		}
	}

	r := f.outboxRunner(f.dispatcher())
	stats, err := r.DeliveryStats(ctx)
	if err != nil {
		t.Fatalf("DeliveryStats: %v", err)
	}
	if stats.Pending != 1 || stats.Failed != 1 {
		t.Errorf("totals = %d pending / %d failed, want 1 / 1", stats.Pending, stats.Failed)
	}
	if len(stats.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %+v, want only the stuck one", stats.Subscriptions)
	}
	got := stats.Subscriptions[0]
	if got.SubscriptionID != stuck.String() || got.TenantID != tenant.String() {
		t.Errorf("entry = %s/%s, want %s/%s", got.TenantID, got.SubscriptionID, tenant, stuck)
	}
	if got.Pending != 1 || got.Failed != 1 {
		t.Errorf("entry counts = %d pending / %d failed, want 1 / 1", got.Pending, got.Failed)
	}
	if got.LastError != newerError || got.LastStatusCode != newerCode || got.LastAttemptAt == "" {
		t.Errorf("entry reason = %q/%d at %q, want the most recent attempt's %q/%d",
			got.LastError, got.LastStatusCode, got.LastAttemptAt, newerError, newerCode)
	}

	pending, err := r.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending != 1 {
		t.Errorf("PendingCount = %d, want 1", pending)
	}
}
