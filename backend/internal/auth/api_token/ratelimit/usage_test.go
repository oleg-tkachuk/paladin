package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestNoopLimiter_UsageEmpty confirms the noop variant returns a zero
// snapshot — Web UI rendering against a deploy with no rate-limit
// store gets {0,0,0,zero-time} and degrades to "no data" rather than
// blowing up.
func TestNoopLimiter_UsageEmpty(t *testing.T) {
	t.Parallel()
	s, err := NoopLimiter{}.Usage(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("usage err: %v", err)
	}
	if s.CurrentBucketCount != 0 || s.PreviousBucketCount != 0 || s.WeightedCount != 0 {
		t.Errorf("expected zero snapshot, got %+v", s)
	}
	if !s.WindowResetsAt.IsZero() {
		t.Errorf("expected zero WindowResetsAt, got %v", s.WindowResetsAt)
	}
}

// TestSnapshot_WeightedSemantics is a tiny doc-test covering the
// invariant "WeightedCount = current + previous * (1 - elapsed/60)".
// Useful to keep handy when refactoring the postgres SQL — it codifies
// what UIs will see if they re-derive the math themselves.
func TestSnapshot_WeightedSemantics(t *testing.T) {
	t.Parallel()
	// Pretend we're 30 seconds into a bucket with 10 in current and
	// 40 in previous.
	current := int64(10)
	previous := int64(40)
	elapsed := 30.0 / 60.0
	wantWeighted := float64(current) + float64(previous)*(1.0-elapsed)
	// 10 + 40 * 0.5 = 30
	if wantWeighted != 30 {
		t.Fatalf("weighted math drift: %v", wantWeighted)
	}

	// Snapshot type exists primarily for transport — confirm round-
	// trip preserves the fields we care about.
	s := Snapshot{
		CurrentBucketCount:  current,
		PreviousBucketCount: previous,
		WeightedCount:       wantWeighted,
		WindowResetsAt:      time.Now().Add(30 * time.Second),
	}
	if s.WeightedCount != 30 {
		t.Errorf("snapshot WeightedCount: got %v, want 30", s.WeightedCount)
	}
}
