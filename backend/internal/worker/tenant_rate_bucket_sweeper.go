package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// TenantRateBucketSweeper drops stale rows from tenant_rate_buckets.
//
// The limiter writes one row per tenant per minute and reads back only two —
// the current bucket and the previous one. Without this the table grows by a
// row per active tenant per minute forever, which is how the last
// counter table quietly became the largest thing in the database.
type TenantRateBucketSweeper struct {
	Store    TenantRateSweeper
	Interval time.Duration
	Logger   *zap.Logger
}

// TenantRateSweeper is the store seam. olderThanMicros keeps the units
// explicit at the boundary — the query casts them into an interval.
type TenantRateSweeper interface {
	SweepTenantRateBuckets(ctx context.Context, olderThanMicros int64) (int64, error)
}

// Run ticks until ctx is done. A zero Interval disables the job, matching
// every other housekeeping worker.
func (s *TenantRateBucketSweeper) Run(ctx context.Context) error {
	if s.Interval <= 0 || s.Store == nil {
		return nil
	}
	// Five minutes of grace on a two-minute working set: enough that a clock
	// skew or a long tick cannot delete a bucket the limiter is still
	// weighting, cheap enough that the table stays small.
	const grace = 5 * time.Minute
	return RunTicker(ctx, "tenant_rate_bucket_sweeper", s.Interval, func(ctx context.Context) error {
		n, err := s.Store.SweepTenantRateBuckets(ctx, grace.Microseconds())
		if err != nil {
			s.log().Warn("failed to sweep tenant rate buckets", zap.Error(err))
			return err
		}
		if n > 0 {
			s.log().Debug("swept stale tenant rate buckets", zap.Int64("rows", n))
		}
		return nil
	})
}

func (s *TenantRateBucketSweeper) log() *zap.Logger {
	if s.Logger == nil {
		return zap.NewNop()
	}
	return s.Logger
}
