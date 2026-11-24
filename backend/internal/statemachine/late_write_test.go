package statemachine

import (
	"errors"
	"testing"
	"time"
)

// A late source write is moved only onto an AVAILABLE object whose recorded
// writes it follows: none since the rebind, and none it does not follow in
// the source's own order.
func TestLateSkipReason(t *testing.T) {
	rebound := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	before, after := rebound.Add(-time.Minute), rebound.Add(time.Minute)
	for name, tc := range map[string]struct {
		row       lateRow
		sequencer string
		want      string
	}{
		"written before the rebind":     {lateRow{state: StateAvailable, writtenAt: &before, follows: true}, "0002", ""},
		"never recorded a write":        {lateRow{state: StateAvailable, follows: true}, "0002", ""},
		"written at the rebind":         {lateRow{state: StateAvailable, writtenAt: &rebound, follows: true}, "0002", LateSkipWrittenSinceRebind},
		"written since the rebind":      {lateRow{state: StateAvailable, writtenAt: &after, follows: true}, "0002", LateSkipWrittenSinceRebind},
		"an older source write":         {lateRow{state: StateAvailable, writtenAt: &before}, "0001", LateSkipNotNewer},
		"no sequencer":                  {lateRow{state: StateAvailable, writtenAt: &before, follows: true}, "", LateSkipNoSequencer},
		"a pending upload":              {lateRow{state: StatePending, follows: true}, "0002", LateSkipNotAvailable},
		"in the trash":                  {lateRow{state: StateDeleted, writtenAt: &before, follows: true}, "0002", LateSkipNotAvailable},
		"since the rebind, out of turn": {lateRow{state: StateAvailable, writtenAt: &after}, "0001", LateSkipWrittenSinceRebind},
	} {
		t.Run(name, func(t *testing.T) {
			if got := lateSkipReason(tc.row, tc.sequencer, rebound); got != tc.want {
				t.Errorf("lateSkipReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLateWriteSkippedErrorMatchesItsSentinel(t *testing.T) {
	err := error(&LateWriteSkippedError{Reason: LateSkipNotNewer})
	if !errors.Is(err, ErrLateWriteSkipped) {
		t.Fatalf("errors.Is(%v, ErrLateWriteSkipped) = false", err)
	}
	if errors.Is(err, ErrNotMigratedFrom) {
		t.Fatalf("a skip matched ErrNotMigratedFrom")
	}
}
