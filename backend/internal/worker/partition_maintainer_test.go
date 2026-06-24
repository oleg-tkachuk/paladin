package worker

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts.UTC()
}

func TestDesiredAhead_Monthly(t *testing.T) {
	spec := PartitionSpec{Table: "audit_log", Period: PeriodMonthly, Ahead: 3}
	got := spec.desiredAhead(mustTime(t, "2026-06-24T12:00:00Z"))
	want := []struct {
		name, from, to string
	}{
		{"audit_log_202606", "2026-06-01", "2026-07-01"},
		{"audit_log_202607", "2026-07-01", "2026-08-01"},
		{"audit_log_202608", "2026-08-01", "2026-09-01"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d defs, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].name != w.name ||
			got[i].from.Format("2006-01-02") != w.from ||
			got[i].to.Format("2006-01-02") != w.to {
			t.Errorf("def %d = {%s %s %s}, want {%s %s %s}", i,
				got[i].name, got[i].from.Format("2006-01-02"), got[i].to.Format("2006-01-02"),
				w.name, w.from, w.to)
		}
	}
}

func TestDesiredAhead_Daily_MonthRollover(t *testing.T) {
	spec := PartitionSpec{Table: "idempotency_keys", Period: PeriodDaily, Ahead: 3}
	got := spec.desiredAhead(mustTime(t, "2026-01-30T23:30:00Z"))
	wantNames := []string{
		"idempotency_keys_20260130",
		"idempotency_keys_20260131",
		"idempotency_keys_20260201", // rolls into February
	}
	for i, w := range wantNames {
		if got[i].name != w {
			t.Errorf("def %d name = %q, want %q", i, got[i].name, w)
		}
	}
}

func TestShouldDrop_Monthly(t *testing.T) {
	// 90-day retention, "now" = 2026-06-24.
	spec := PartitionSpec{Table: "audit_log", Period: PeriodMonthly, Retention: 90 * 24 * time.Hour}
	now := mustTime(t, "2026-06-24T00:00:00Z")
	cases := []struct {
		name string
		drop bool
	}{
		// Feb partition upper bound = Mar 1 = 115 days before now → drop.
		{"audit_log_202602", true},
		// March upper bound = Apr 1 = 84 days before now → within retention.
		{"audit_log_202603", false},
		// Current month → never.
		{"audit_log_202606", false},
		// Future → never.
		{"audit_log_202607", false},
		// Default catch-all → never.
		{"audit_log_default", false},
		// Unparseable → never (defensive).
		{"audit_log_garbage", false},
	}
	for _, c := range cases {
		if got := spec.shouldDrop(c.name, now); got != c.drop {
			t.Errorf("shouldDrop(%q) = %v, want %v", c.name, got, c.drop)
		}
	}
}

func TestShouldDrop_Daily_ZeroRetention(t *testing.T) {
	// Retention 0: drop a day's partition as soon as the day is fully past.
	spec := PartitionSpec{Table: "idempotency_keys", Period: PeriodDaily, Retention: 0}
	now := mustTime(t, "2026-06-24T08:00:00Z")
	cases := []struct {
		name string
		drop bool
	}{
		{"idempotency_keys_20260622", true},  // upper = 06-23 <= now → drop
		{"idempotency_keys_20260623", true},  // upper = 06-24 00:00 <= now 08:00 → drop
		{"idempotency_keys_20260624", false}, // current day, upper = 06-25 > now
		{"idempotency_keys_20260625", false}, // future
		{"idempotency_keys_default", false},
	}
	for _, c := range cases {
		if got := spec.shouldDrop(c.name, now); got != c.drop {
			t.Errorf("shouldDrop(%q) = %v, want %v", c.name, got, c.drop)
		}
	}
}

func TestShouldDrop_NegativeRetentionNeverDrops(t *testing.T) {
	// audit TTL=0 -> Retention<0 -> keep forever, even ancient partitions.
	spec := PartitionSpec{Table: "audit_log", Period: PeriodMonthly, Retention: -1}
	now := mustTime(t, "2026-06-24T00:00:00Z")
	if spec.shouldDrop("audit_log_201901", now) {
		t.Error("dropped a partition with retention disabled")
	}
}

// A different parent's partition name must not be mistaken for ours.
func TestShouldDrop_ForeignPrefix(t *testing.T) {
	spec := PartitionSpec{Table: "audit_log", Period: PeriodMonthly, Retention: 0}
	now := mustTime(t, "2026-06-24T00:00:00Z")
	if spec.shouldDrop("idempotency_keys_20240101", now) {
		t.Error("dropped a partition belonging to a different table")
	}
}
