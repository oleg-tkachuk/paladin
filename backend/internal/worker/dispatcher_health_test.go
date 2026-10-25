package worker

import (
	"context"
	"errors"
	"testing"
)

func TestOutboxComponent(t *testing.T) {
	var n int64 = 4
	var fail error
	c := OutboxComponent(func(context.Context) (int64, error) { return n, fail })
	if err := c.Func(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := detailsOf(c); d["/events waiting"] != "4" {
		t.Errorf("details %v", d)
	}
	fail = errors.New("table gone")
	if err := c.Func(context.Background()); !errors.Is(err, fail) {
		t.Errorf("an unreadable outbox: %v", err)
	}
	if d := c.Describe(context.Background()); d != nil {
		t.Errorf("details of a failed read %v, want none", d)
	}
}

// A backlog is not a failure: the component says how big it is and stays
// healthy; only a failed read is one.
func TestDeliveriesComponent(t *testing.T) {
	stats := &DeliveryStats{Pending: 3, Failed: 1, OldestPendingSeconds: 90, Subscriptions: make([]SubscriptionDelivStat, 2)}
	c := DeliveriesComponent(func(context.Context) (*DeliveryStats, error) { return stats, nil })
	if c.Critical {
		t.Error("late deliveries take the pod out of service")
	}
	if err := c.Func(context.Background()); err != nil {
		t.Fatalf("a backlog failed the check: %v", err)
	}
	d := detailsOf(c)
	if d["queue/pending"] != "3" || d["queue/failed"] != "1" || d["queue/subscriptions stuck"] != "2" || d["/oldest pending"] != "1m30s" {
		t.Errorf("details %v", d)
	}
	stats = &DeliveryStats{}
	_ = c.Func(context.Background())
	if d := detailsOf(c); d["/oldest pending"] != "none" {
		t.Errorf("nothing pending: oldest %q, want none", d["/oldest pending"])
	}

	boom := errors.New("boom")
	failing := DeliveriesComponent(func(context.Context) (*DeliveryStats, error) { return nil, boom })
	if err := failing.Func(context.Background()); !errors.Is(err, boom) {
		t.Errorf("a failed read: %v", err)
	}
}
