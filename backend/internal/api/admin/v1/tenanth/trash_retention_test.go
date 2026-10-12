package tenanth

import (
	"testing"
	"time"
)

func TestRetentionLapsesAt(t *testing.T) {
	const retention = 30 * 24 * time.Hour
	deleted := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	if got := (Tenant{}).RetentionLapsesAt(retention); !got.IsZero() {
		t.Errorf("active tenant: RetentionLapsesAt = %v, want zero", got)
	}
	// Exactly deleted_at + retention — not a day, an hour or a tick off. The
	// console marks the tenant from this instant on (inclusive).
	trashed := Tenant{DeletedAt: deleted}
	if got, want := trashed.RetentionLapsesAt(retention), deleted.Add(retention); !got.Equal(want) {
		t.Errorf("trashed tenant: RetentionLapsesAt = %v, want %v", got, want)
	}
	if got := trashed.RetentionLapsesAt(time.Nanosecond); !got.Equal(deleted.Add(time.Nanosecond)) {
		t.Errorf("1ns retention: RetentionLapsesAt = %v, want deleted_at+1ns", got)
	}
	// A retention that is not positive must not mark every trashed tenant as
	// due the moment it lands in the trash.
	for _, r := range []time.Duration{0, -time.Hour} {
		if got := trashed.RetentionLapsesAt(r); !got.IsZero() {
			t.Errorf("retention %v: RetentionLapsesAt = %v, want zero", r, got)
		}
	}
}
