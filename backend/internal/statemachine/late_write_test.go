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
			// A forward move does not read the event's time.
			w := LateSourceWrite{Sequencer: tc.sequencer}
			if got := lateSkipReason(tc.row, w, lateMove{movedAt: rebound, reboundAt: rebound}); got != tc.want {
				t.Errorf("lateSkipReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A late target write after a rollback is moved only when its event reports
// a write made after the rebind — the target received the migration's own
// copies before it, and the clocks differ by up to LateWriteClockMargin — and
// the row records nothing since the rebind back.
func TestLateSkipReasonAfterTheRebindBack(t *testing.T) {
	rebound := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	reboundBack := rebound.Add(time.Hour)
	beforeRebind, sinceBack := rebound.Add(-time.Minute), reboundBack.Add(time.Minute)
	rollingBack, withinMargin := rebound.Add(2*LateWriteClockMargin), rebound.Add(LateWriteClockMargin/2)
	back := lateMove{movedAt: reboundBack, reboundAt: rebound, back: true}
	for name, tc := range map[string]struct {
		row       lateRow
		writtenAt time.Time
		want      string
	}{
		"a write while rolling back":       {lateRow{state: StateAvailable, writtenAt: &beforeRebind, follows: true}, rollingBack, ""},
		"a write at the margin":            {lateRow{state: StateAvailable, follows: true}, rebound.Add(LateWriteClockMargin), ""},
		"a write at the rebind":            {lateRow{state: StateAvailable, follows: true}, rebound, LateSkipBeforeRebind},
		"within the clock margin":          {lateRow{state: StateAvailable, follows: true}, withinMargin, LateSkipBeforeRebind},
		"recorded in the target before it": {lateRow{state: StateAvailable, writtenAt: &rollingBack, follows: true}, rollingBack, ""},
		"the migration's copy":             {lateRow{state: StateAvailable, writtenAt: &beforeRebind, follows: true}, beforeRebind, LateSkipBeforeRebind},
		"no time reported":                 {lateRow{state: StateAvailable, follows: true}, time.Time{}, LateSkipNoEventTime},
		"written since the rebind back":    {lateRow{state: StateAvailable, writtenAt: &sinceBack, follows: true}, rollingBack, LateSkipWrittenSinceRebind},
		"written at the rebind back":       {lateRow{state: StateAvailable, writtenAt: &reboundBack, follows: true}, rollingBack, LateSkipWrittenSinceRebind},
		"an older target write":            {lateRow{state: StateAvailable, writtenAt: &rollingBack}, rollingBack, LateSkipNotNewer},
		"in the trash":                     {lateRow{state: StateDeleted, follows: true}, rollingBack, LateSkipNotAvailable},
	} {
		t.Run(name, func(t *testing.T) {
			w := LateSourceWrite{Sequencer: "0002", WrittenAt: tc.writtenAt}
			if got := lateSkipReason(tc.row, w, back); got != tc.want {
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
