// Package postgres is the Limiter backed by api_token_rate_buckets
// (migration 018). One atomic SQL statement does the increment + read
// of both the current and previous bucket so callers see a consistent
// weighted count without round-trip-multiplied races.
//
// The increment is unconditional — even denied requests count toward
// the bucket. That keeps logic simple and produces the desired
// "rate-limited callers don't get to spend future quota on rejected
// calls" behaviour. The marginal over-attribution (a denied request
// adds to the next bucket's weighted history) is negligible against
// the 95%-accurate sliding-window approximation.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin-private/internal/auth/api_token/ratelimit"
)

// Limiter implements ratelimit.Limiter against api_token_rate_buckets.
type Limiter struct {
	pool *pgxpool.Pool
}

// New constructs a Limiter. Caller owns pool lifecycle.
func New(pool *pgxpool.Pool) (*Limiter, error) {
	if pool == nil {
		return nil, errors.New("ratelimit/postgres: pool required")
	}
	return &Limiter{pool: pool}, nil
}

// Allow implements ratelimit.Limiter.
//
// The single statement:
//
//  1. Bumps the current minute bucket (INSERT ON CONFLICT ... count+1).
//  2. Reads the previous minute bucket count (defaulting to 0 when
//     no row exists yet — typical for a fresh token).
//  3. Computes the weighted count using the elapsed-in-current ratio.
//
// Decision.Allowed is computed in Go from the returned weighted count
// vs the supplied capacity. RetryAfter = bucket_end - now in the
// denied case.
func (l *Limiter) Allow(ctx context.Context, tokenID uuid.UUID, capacity int) (ratelimit.Decision, error) {
	if capacity <= 0 {
		// Unlimited fast-path. Don't touch the table.
		return ratelimit.Decision{Allowed: true}, nil
	}

	const stmt = `
WITH
  cur_start AS (SELECT date_trunc('minute', NOW()) AS s),
  bumped AS (
    INSERT INTO api_token_rate_buckets (token_id, bucket_start, count)
    VALUES ($1, (SELECT s FROM cur_start), 1)
    ON CONFLICT (token_id, bucket_start) DO UPDATE
      SET count = api_token_rate_buckets.count + 1
    RETURNING count
  ),
  prev_count AS (
    SELECT COALESCE(SUM(count), 0)::bigint AS c
    FROM api_token_rate_buckets
    WHERE token_id = $1
      AND bucket_start = (SELECT s - interval '1 minute' FROM cur_start)
  ),
  elapsed AS (
    SELECT EXTRACT(EPOCH FROM NOW() - (SELECT s FROM cur_start))::float8 AS sec
  )
SELECT
  (SELECT count FROM bumped)::float8
    + (SELECT c FROM prev_count)::float8 * (1.0 - (SELECT sec FROM elapsed) / 60.0)
    AS weighted_count,
  (60.0 - (SELECT sec FROM elapsed))::float8 AS retry_after_seconds;
`

	var weighted, retryAfter float64
	if err := l.pool.QueryRow(ctx, stmt, tokenID).Scan(&weighted, &retryAfter); err != nil {
		return ratelimit.Decision{}, fmt.Errorf("ratelimit/postgres: bump: %w", err)
	}

	allowed := weighted <= float64(capacity)
	d := ratelimit.Decision{
		Allowed:       allowed,
		WeightedCount: weighted,
	}
	if !allowed {
		// Round up so a 0.4s remaining returns 1s — clients shouldn't
		// retry mid-bucket only to be told no again.
		d.RetryAfter = time.Duration((retryAfter*1e9)+1) * time.Nanosecond
		if d.RetryAfter < time.Second {
			d.RetryAfter = time.Second
		}
	}
	return d, nil
}

// Usage implements ratelimit.Limiter — readonly snapshot. Reads both
// buckets via a single round-trip; computes weighted_count + window
// reset time. Crucially does NOT bump anything: Web UI dashboards can
// poll this every few seconds without distorting the rate counters
// they're trying to display.
func (l *Limiter) Usage(ctx context.Context, tokenID uuid.UUID) (ratelimit.Snapshot, error) {
	const stmt = `
WITH
  cur_start AS (SELECT date_trunc('minute', NOW()) AS s)
SELECT
  COALESCE((
    SELECT count FROM api_token_rate_buckets
    WHERE token_id = $1 AND bucket_start = (SELECT s FROM cur_start)
  ), 0)::bigint AS current_count,
  COALESCE((
    SELECT count FROM api_token_rate_buckets
    WHERE token_id = $1 AND bucket_start = (SELECT s - interval '1 minute' FROM cur_start)
  ), 0)::bigint AS previous_count,
  EXTRACT(EPOCH FROM NOW() - (SELECT s FROM cur_start))::float8 AS elapsed_seconds,
  (SELECT s + interval '1 minute' FROM cur_start)::timestamptz AS resets_at;
`
	var (
		curCount, prevCount int64
		elapsed             float64
		resetsAt            time.Time
	)
	if err := l.pool.QueryRow(ctx, stmt, tokenID).
		Scan(&curCount, &prevCount, &elapsed, &resetsAt); err != nil {
		return ratelimit.Snapshot{}, fmt.Errorf("ratelimit/postgres: usage: %w", err)
	}
	weighted := float64(curCount) + float64(prevCount)*(1.0-elapsed/60.0)
	return ratelimit.Snapshot{
		CurrentBucketCount:  curCount,
		PreviousBucketCount: prevCount,
		WeightedCount:       weighted,
		WindowResetsAt:      resetsAt.UTC(),
	}, nil
}

// Sweep implements ratelimit.Limiter. Drops bucket rows older than
// (now - olderThan). Called from the api_token purger.
func (l *Limiter) Sweep(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = 5 * time.Minute
	}
	const stmt = `
DELETE FROM api_token_rate_buckets
WHERE bucket_start < NOW() - ($1::bigint || ' microseconds')::interval;
`
	tag, err := l.pool.Exec(ctx, stmt, olderThan.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("ratelimit/postgres: sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}
