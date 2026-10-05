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
