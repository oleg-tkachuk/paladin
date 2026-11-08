//go:build integration

// The dispatcher's fan-out stage (ADR-0044): which subscriptions receive a
// recorded event, and that the event leaves event_outbox once fanned out.
package integration

import (
	"context"
	"net/http"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// recordEvent records evt for tenant on a transaction of its own, as a
// producer does, without fanning it out.
func (f *dispatcherFixture) recordEvent(t *testing.T, tenant uuid.UUID, evt worker.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.h.PoolMigrate.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := worker.NewEventRecorder(nil).RecordTx(ctx, tx, tenant.String(), evt); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("RecordTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func (f *dispatcherFixture) recordedEvents(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.h.PoolMigrate.QueryRow(context.Background(),
		`SELECT count(*) FROM event_outbox WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count recorded events: %v", err)
	}
	return n
}

func (f *dispatcherFixture) fanOut(t *testing.T) int {
	t.Helper()
	n, err := f.outboxRunner(f.dispatcher()).FanOut(context.Background())
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}
	return n
}

// A recorded event reaches each subscription that existed when it was
// recorded, and leaves event_outbox in the same pass.
func TestFanOut_QueuesADeliveryPerSubscriptionAndConsumesTheEvent(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "fanout-basic")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	f.recordEvent(t, tenant, makeEvent("", tenant))
	if got := f.recordedEvents(t, tenant); got != 1 {
		t.Fatalf("recorded events = %d, want 1", got)
	}
	if rows := f.allDeliveryRows(t, tenant); len(rows) != 0 {
		t.Fatalf("delivery rows before fan-out = %d, want 0 — the producer must not fan out", len(rows))
	}

	if n := f.fanOut(t); n < 1 {
		t.Fatalf("fanned out = %d, want at least this tenant's event", n)
	}
	if rows := f.allDeliveryRows(t, tenant); len(rows) != 2 {
		t.Errorf("delivery rows = %d, want 2", len(rows))
	}
	if got := f.recordedEvents(t, tenant); got != 0 {
		t.Errorf("recorded events after fan-out = %d, want 0", got)
	}
}

// A subscription created after the event was recorded does not receive it.
func TestFanOut_ASubscriptionCreatedLaterGetsNothing(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "fanout-later")

	f.recordEvent(t, tenant, makeEvent("", tenant))
	f.seedSubscription(t, tenant, subOpts{URL: "http://unused.invalid"})
	f.fanOut(t)

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 0 {
		t.Errorf("delivery rows = %d, want 0", len(rows))
	}
	if got := f.recordedEvents(t, tenant); got != 0 {
		t.Errorf("recorded events = %d, want 0 — an event nobody receives is still consumed", got)
	}
}

// Deleting a subscription between the event and its fan-out fails nothing:
// the event commits with its producer, and the deleted subscription simply
// receives nothing. In-transaction fan-out failed the producer here.
func TestFanOut_ASubscriptionDeletedBeforeFanOutFailsNothing(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "fanout-deleted")
	gone := f.seedSubscription(t, tenant, subOpts{URL: "http://unused.invalid"})
	kept := f.seedSubscription(t, tenant, subOpts{URL: "http://unused.invalid"})

	f.recordEvent(t, tenant, makeEvent("", tenant))
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`DELETE FROM event_subscriptions WHERE id = $1`, gone); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}
	f.fanOut(t)

	var subs []uuid.UUID
	rows, err := f.h.PoolMigrate.Query(context.Background(),
		`SELECT subscription_id FROM event_deliveries WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		subs = append(subs, id)
	}
	rows.Close()
	if len(subs) != 1 || subs[0] != kept {
		t.Errorf("deliveries go to %v, want only the subscription that remains (%v)", subs, kept)
	}
}
