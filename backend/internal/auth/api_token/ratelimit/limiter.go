// Package ratelimit is the per-token sliding-window rate limiter. The
// Postgres-backed implementation lives in subpackage ./postgres; this
// file holds the interface and the typed result so callers (the
// APITokenInterceptor) don't import the storage tier directly.
//
// Algorithm: sliding-window counter (the Cloudflare / Redis-Cell
// approach). Each token has 1-minute fixed buckets; the effective
// count for "the last 60 seconds" is:
//
//	weighted = current_bucket_count + previous_bucket_count *
//	           (1.0 - elapsed_in_current_bucket / 60.0)
//
// Accuracy is ~95% — the worst case is when a burst lands at the
// bucket boundary, which the weighting smooths out. Storage is O(2
// rows per active token); the purger drops rows older than 5 minutes.
package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Decision is the typed result of Allow.
type Decision struct {
	// Allowed is true when the request fits within the cap.
	Allowed bool

	// WeightedCount is the effective request count over the trailing
	// 60s after this request was attempted (always reflects the new
	// state — Allow always increments). Surfaced for observability /
	// dashboards; not load-bearing.
	WeightedCount float64

	// RetryAfter is roughly when the caller should try again. For a
	// 1-minute bucket: bucket_end - now. Implementations may return
	// 0 when not applicable (e.g. unlimited tokens).
	RetryAfter time.Duration
}

// Snapshot is the readonly view of a token's current rate-limit state.
// Returned by Limiter.Usage; never mutates the store. Web UI consumes
// this to render progress bars without taking the verify-path
// shortcut of always bumping the bucket.
type Snapshot struct {
	// CurrentBucketCount is the raw counter for the in-progress
	// minute. Bumped by every Allow call (allowed or denied).
	CurrentBucketCount int64

	// PreviousBucketCount is the raw counter for the previous full
	// minute. Used by Allow's weighted-window calculation.
	PreviousBucketCount int64

	// WeightedCount is the same number Allow returns — current +
	// previous * (1 - elapsed_in_current / 60). Surfaced here so UI
	// callers don't have to redo the math.
	WeightedCount float64

	// WindowResetsAt is when the current minute bucket rolls — i.e.
	// when the bucket counter starts fresh and the previous bucket
	// becomes the new "previous". Useful for the "resets in Ns" UI.
	WindowResetsAt time.Time
}

// Limiter gates per-token request rates. The interceptor calls Allow
// after a successful Verify; on Allowed=false the interceptor returns
// connect.CodeResourceExhausted with a Retry-After header.
type Limiter interface {
	// Allow checks the token's bucket, increments the counter, and
	// returns the Decision. capacity is the per-minute ceiling
	// (api_tokens.rate_limit_rpm); 0 means unlimited and the
	// implementation must short-circuit without touching the store.
	Allow(ctx context.Context, tokenID uuid.UUID, capacity int) (Decision, error)

	// Usage returns the readonly Snapshot for a token. Does NOT bump
	// the bucket — distinct from Allow which always bumps. Cheap
	// query, suitable for UI dashboards refreshing every few seconds.
	Usage(ctx context.Context, tokenID uuid.UUID) (Snapshot, error)

	// Sweep drops bucket rows older than the supplied grace window.
	// Called by the api_token purger; safe to invoke from a goroutine.
	Sweep(ctx context.Context, olderThan time.Duration) (int64, error)
}

// ErrUnconfigured is returned when callers wire a nil Limiter and
// attempt to Allow. Distinct from "Allowed=false" so the interceptor
// can fail-closed (or fall through) deterministically.
var ErrUnconfigured = errors.New("ratelimit: limiter not configured")

// NoopLimiter is the zero-cost implementation used when the api_token
// subsystem is enabled but rate limiting is not. Allows every request,
// returns RetryAfter=0.
type NoopLimiter struct{}

// Allow implements Limiter.
func (NoopLimiter) Allow(context.Context, uuid.UUID, int) (Decision, error) {
	return Decision{Allowed: true}, nil
}

// Usage implements Limiter — empty snapshot for the noop variant.
// Callers that wire NoopLimiter shouldn't be surfacing UI progress
// bars in the first place.
func (NoopLimiter) Usage(context.Context, uuid.UUID) (Snapshot, error) {
	return Snapshot{}, nil
}

// Sweep implements Limiter.
func (NoopLimiter) Sweep(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
