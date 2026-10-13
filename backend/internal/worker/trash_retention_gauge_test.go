package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeTrashCounter struct {
	n         int64
	err       error
	retention time.Duration
	asOf      time.Time
}

func (f *fakeTrashCounter) CountTenantsPastTrashRetention(_ context.Context, retention time.Duration, asOf time.Time) (int64, error) {
	f.retention, f.asOf = retention, asOf
	return f.n, f.err
}

const trashGaugeName = "paladin.tenants.past_trash_retention"

// A tick counts with the configured retention as of its own clock, and the
// gauge carries the count — back to zero once the trash is decided on.
func TestTrashRetentionGaugePublishesTheCount(t *testing.T) {
	const retention = 30 * 24 * time.Hour
	asOf := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store := &fakeTrashCounter{n: 3}
	g := &TrashRetentionGauge{Store: store, Retention: retention, Now: func() time.Time { return asOf }}

	if err := g.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if store.retention != retention || !store.asOf.Equal(asOf) {
		t.Errorf("counted with %v as of %v, want %v as of %v", store.retention, store.asOf, retention, asOf)
	}
	if n, _, ok := gauge[int64](t, trashGaugeName); !ok || n != 3 {
		t.Errorf("%s = %d (found %v), want 3", trashGaugeName, n, ok)
	}

	store.n = 0
	if err := g.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n, _, _ := gauge[int64](t, trashGaugeName); n != 0 {
		t.Errorf("after the trash is cleared, %s = %d, want 0", trashGaugeName, n)
	}
}

// A failed count fails the tick and leaves the last value, rather than
// publishing a 0 that reads as "nothing to decide on".
func TestTrashRetentionGaugeKeepsTheLastValueOnError(t *testing.T) {
	store := &fakeTrashCounter{n: 5}
	g := &TrashRetentionGauge{Store: store, Retention: time.Hour}
	if err := g.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	store.err = errors.New("db down")
	if err := g.tick(context.Background()); err == nil {
		t.Error("a failed count did not fail the tick")
	}
	if n, _, _ := gauge[int64](t, trashGaugeName); n != 5 {
		t.Errorf("%s = %d after a failed count, want the last good 5", trashGaugeName, n)
	}
}

// Off without an interval or a retention, as every housekeeping job is.
func TestTrashRetentionGaugeDisabled(t *testing.T) {
	for name, g := range map[string]*TrashRetentionGauge{
		"no interval":  {Store: &fakeTrashCounter{}, Retention: time.Hour},
		"no retention": {Store: &fakeTrashCounter{}, Interval: time.Minute},
		"no store":     {Retention: time.Hour, Interval: time.Minute},
	} {
		if err := g.Run(context.Background()); err != nil {
			t.Errorf("%s: Run = %v, want an immediate nil", name, err)
		}
	}
}
