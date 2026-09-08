package eventingest

import (
	"context"
	"testing"
	"time"
)

// The reaper bounds the dedup table, and the dedup table is what makes
// at-least-once delivery safe: every event passes through ingested_events by
// id before the handler runs. Purge it too eagerly and a re-delivered event
// is no longer recognised as a duplicate — the handler runs again, the object
// is promoted again, and the outbox fans the upload out to subscribers a
// second time. Nothing errors; the pipeline reports success both times.
//
// Which makes `if r.TTL <= 0 { r.TTL = 24h }` load-bearing. Relaxed to
// `< 0` it still reads as a bounds check, and a config that omits the TTL
// sweeps with a cutoff of *now* — deleting every dedup row on every pass.
func TestReaperRunNormalisesUnsetFields(t *testing.T) {
	// A cancelled context returns from the select before the first tick, so
	// Run does its normalisation and nothing else. No queries, no database.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("unset means the default", func(t *testing.T) {
		r := &Reaper{}
		if err := r.Run(ctx); err == nil {
			t.Fatal("Run returned nil on a cancelled context, want ctx.Err()")
		}
		if r.Interval != time.Hour {
			t.Errorf("Interval = %v, want 1h", r.Interval)
		}
		if r.TTL != 24*time.Hour {
			t.Errorf("TTL = %v, want 24h — a zero TTL sweeps with a cutoff of now "+
				"and empties the dedup table every pass", r.TTL)
		}
	})

	t.Run("negative means the default too", func(t *testing.T) {
		r := &Reaper{Interval: -time.Second, TTL: -time.Second}
		_ = r.Run(ctx)
		if r.Interval != time.Hour || r.TTL != 24*time.Hour {
			t.Errorf("negatives = (%v, %v), want the defaults", r.Interval, r.TTL)
		}
	})

	t.Run("configured values survive", func(t *testing.T) {
		r := &Reaper{Interval: 5 * time.Minute, TTL: 90 * time.Minute}
		_ = r.Run(ctx)
		if r.Interval != 5*time.Minute {
			t.Errorf("Interval = %v, want the configured 5m", r.Interval)
		}
		if r.TTL != 90*time.Minute {
			t.Errorf("TTL = %v, want the configured 90m", r.TTL)
		}
	})
}

// A nil logger is the wiring a pod gets when nobody passes one, and the
// reaper reaches for it on the purge-error path — where a nil deref would
// turn a transient database error into a crashed pod.
func TestReaperLogIsNilSafe(t *testing.T) {
	r := &Reaper{}
	if r.log() == nil {
		t.Fatal("log() returned nil")
	}
	r.log().Warn("must not panic")
}
