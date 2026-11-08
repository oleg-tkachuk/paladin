package worker

import (
	"testing"
	"time"
	"uuid"
)

// A subscription receives an event only if it existed when the event was
// recorded (ADR-0044): one created afterwards does not, one created at the
// same instant does.
func TestSubscribers_OnlyThoseThatExistedWhenRecorded(t *testing.T) {
	tenant := uuid.NewV7()
	recordedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	before := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt.Add(-time.Minute)}
	same := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt}
	after := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt.Add(time.Microsecond)}

	got := (&Dispatcher{}).subscribers([]fanoutSubscription{before, same, after},
		Event{Type: "paladin.object.uploaded"}, recordedAt)

	want := []uuid.UUID{before.ID, same.ID}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("subscribers = %v, want %v", got, want)
	}
}

// Each subscription is matched on its own filter: one event can reach some
// of a tenant's subscriptions and not others.
func TestSubscribers_EachOnItsOwnFilter(t *testing.T) {
	tenant := uuid.NewV7()
	recordedAt := time.Now()
	uploads := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt,
		CELFilter: `type == "paladin.object.uploaded"`}
	deletes := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt,
		CELFilter: `type == "paladin.object.deleted"`}
	all := fanoutSubscription{ID: uuid.NewV7(), TenantID: tenant, CreatedAt: recordedAt}

	got := (&Dispatcher{}).subscribers([]fanoutSubscription{uploads, deletes, all},
		Event{Type: "paladin.object.uploaded"}, recordedAt)

	if len(got) != 2 || got[0] != uploads.ID || got[1] != all.ID {
		t.Errorf("subscribers = %v, want [%v %v]", got, uploads.ID, all.ID)
	}
}

// No subscription, no delivery.
func TestSubscribers_NoneWithoutSubscriptions(t *testing.T) {
	if got := (&Dispatcher{}).subscribers(nil, Event{Type: "paladin.object.uploaded"}, time.Now()); len(got) != 0 {
		t.Errorf("subscribers = %v, want none", got)
	}
}
