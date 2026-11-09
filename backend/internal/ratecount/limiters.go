package ratecount

import (
	"context"
	"fmt"
	"time"

	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// TenantStore backs middleware.TenantRateLimitInterceptor with a Counter.
type TenantStore struct{ counter Counter }

// NewTenantStore wires the adapter.
func NewTenantStore(c Counter) *TenantStore { return &TenantStore{counter: c} }

// BumpTenantRate counts one request of the tenant and returns the weighted
// trailing count and the seconds until its bucket rolls.
func (s *TenantStore) BumpTenantRate(ctx context.Context, tenantID uuid.UUID) (float64, float64, error) {
	c, err := s.counter.Bump(ctx, TenantKey(tenantID))
	if err != nil {
		return 0, 0, fmt.Errorf("ratecount: bump tenant: %w", err)
	}
	return c.Weighted(), (Window - c.Elapsed).Seconds(), nil
}

// TokenLimiter is a ratelimit.Limiter on a Counter.
type TokenLimiter struct{ counter Counter }

// NewTokenLimiter wires the adapter.
func NewTokenLimiter(c Counter) *TokenLimiter { return &TokenLimiter{counter: c} }

// Allow implements ratelimit.Limiter. Only an admitted request is counted,
// so the Retry-After a denial carries is exact.
func (l *TokenLimiter) Allow(ctx context.Context, tokenID uuid.UUID, capacity int) (ratelimit.Decision, error) {
	if capacity <= 0 {
		return ratelimit.Decision{Allowed: true}, nil
	}
	c, err := l.counter.BumpIfFits(ctx, TokenKey(tokenID), capacity)
	if err != nil {
		return ratelimit.Decision{}, fmt.Errorf("ratecount: bump token: %w", err)
	}
	if c.Admitted {
		return ratelimit.Decision{Allowed: true, WeightedCount: c.Weighted()}, nil
	}
	return ratelimit.Decision{
		WeightedCount: ratelimit.Weighted(c.Current+1, c.Previous, c.Elapsed),
		RetryAfter:    ratelimit.RetryAfter(c.Current, c.Previous, c.Elapsed, capacity),
	}, nil
}

// Usage implements ratelimit.Limiter without counting anything.
func (l *TokenLimiter) Usage(ctx context.Context, tokenID uuid.UUID) (ratelimit.Snapshot, error) {
	c, err := l.counter.Read(ctx, TokenKey(tokenID))
	if err != nil {
		return ratelimit.Snapshot{}, fmt.Errorf("ratecount: read token: %w", err)
	}
	return ratelimit.Snapshot{
		CurrentBucketCount:  c.Current,
		PreviousBucketCount: c.Previous,
		WeightedCount:       c.Weighted(),
		WindowResetsAt:      c.ResetsAt(),
	}, nil
}

// Sweep implements ratelimit.Limiter. A Counter's windows expire on their
// own, so there is nothing to sweep.
func (*TokenLimiter) Sweep(context.Context, time.Duration) (int64, error) { return 0, nil }
