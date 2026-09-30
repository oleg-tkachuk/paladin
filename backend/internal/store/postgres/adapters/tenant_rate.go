package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TenantRateStore backs middleware.TenantRateLimitInterceptor with
// tenant_rate_buckets, so every replica counts against one window instead of
// each keeping its own.
type TenantRateStore struct {
	q *sqlc.Queries
}

// NewTenantRateStore wires the adapter.
func NewTenantRateStore(q *sqlc.Queries) *TenantRateStore {
	return &TenantRateStore{q: q}
}

// BumpTenantRate increments the tenant's current minute and returns the
// weighted count over the trailing 60 seconds plus the seconds left in the
// bucket. One statement: the read of the previous bucket happens inside the
// same query as the write, so two concurrent requests cannot both read a
// count that neither has yet incremented.
func (s *TenantRateStore) BumpTenantRate(ctx context.Context, tenantID uuid.UUID) (float64, float64, error) {
	row, err := s.q.BumpTenantRateBucket(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		return 0, 0, fmt.Errorf("adapters: bump tenant rate bucket: %w", err)
	}
	return row.WeightedCount, row.RetryAfterSeconds, nil
}

// SweepTenantRateBuckets drops buckets older than the supplied age. Two are
// live per active tenant; the rest is history nothing reads, and without this
// the table grows by one row per tenant per minute forever.
func (s *TenantRateStore) SweepTenantRateBuckets(ctx context.Context, olderThanMicros int64) (int64, error) {
	n, err := s.q.SweepTenantRateBuckets(ctx, olderThanMicros)
	if err != nil {
		return 0, fmt.Errorf("adapters: sweep tenant rate buckets: %w", err)
	}
	return n, nil
}
