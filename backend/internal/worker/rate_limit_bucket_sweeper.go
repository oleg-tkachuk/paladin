package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// RateLimitBucketSweeper drops stale rows from rate_limit_buckets.
//
// The rate limits write one row per tenant or token per minute and read back
// only two — the current bucket and the previous one (ADR-0046). Without this
// the table grows by a row per active subject per minute forever, which is
// how the last counter table quietly became the largest thing in the database.
type RateLimitBucketSweeper struct {
	Store    RateLimitSweeper
	Interval time.Duration
	Logger   *zap.Logger
}

// RateLimitSweeper is the store seam.
type RateLimitSweeper interface {
	SweepRateLimitBuckets(ctx context.Context, olderThan time.Duration) (int64, error)
}

// Run ticks until ctx is done. A zero Interval disables the job, matching
// every other housekeeping worker.
func (s *RateLimitBucketSweeper) Run(ctx context.Context) error {
	if s.Interval <= 0 || s.Store == nil {
		return nil
	}
	// Five minutes of grace on a two-minute working set: enough that a clock
	// skew or a long tick cannot delete a bucket the limiter is still
	// weighting, cheap enough that the table stays small.
	const grace = 5 * time.Minute
	return RunTicker(ctx, "rate_limit_bucket_sweeper", s.Interval, func(ctx context.Context) error {
		n, err := s.Store.SweepRateLimitBuckets(ctx, grace)
		if err != nil {
			s.log().Warn("failed to sweep rate limit buckets", zap.Error(err))
			return err
		}
		if n > 0 {
			s.log().Debug("swept stale rate limit buckets", zap.Int64("rows", n))
		}
		return nil
	})
}

func (s *RateLimitBucketSweeper) log() *zap.Logger {
	if s.Logger == nil {
		return zap.NewNop()
	}
	return s.Logger
}
