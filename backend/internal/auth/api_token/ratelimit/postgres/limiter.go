// Package postgres is the Limiter backed by api_token_rate_buckets
// (the schema baseline (001_initial_schema.sql)). One atomic SQL statement does the increment + read
// of both the current and previous bucket so callers see a consistent
// weighted count without round-trip-multiplied races.
//
// Only an admitted request is counted. A denied one adds nothing, so the
// Retry-After a denial carries holds: a caller that waits it out gets in,
// however often it asked meanwhile. Counting denials made every retry push the
// window further out, and a client honouring Retry-After was refused again.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
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
// One statement reads both buckets and bumps the current one only when the
// request fits: the insert proposes a row only if the request fits an empty
// current bucket, and the conflict update re-checks against the locked row,
// so concurrent requests cannot both take the last slot. The previous
// bucket's share and the elapsed time come from the same NOW().
func (l *Limiter) Allow(ctx context.Context, tokenID uuid.UUID, capacity int) (ratelimit.Decision, error) {
	if capacity <= 0 {
		// Unlimited fast-path. Don't touch the table.
		return ratelimit.Decision{Allowed: true}, nil
	}

	const stmt = `
WITH
  cur_start AS (SELECT date_trunc('minute', NOW()) AS s),
  elapsed AS (
    SELECT EXTRACT(EPOCH FROM NOW() - (SELECT s FROM cur_start))::float8 AS sec
  ),
  prev_count AS (
    SELECT COALESCE(SUM(count), 0)::bigint AS c
    FROM api_token_rate_buckets
    WHERE token_id = $1
      AND bucket_start = (SELECT s - interval '1 minute' FROM cur_start)
  ),
  cur_count AS (
    SELECT COALESCE(SUM(count), 0)::bigint AS c
    FROM api_token_rate_buckets
    WHERE token_id = $1
      AND bucket_start = (SELECT s FROM cur_start)
  ),
  prev_share AS (
    SELECT (SELECT c FROM prev_count)::float8
      * (1.0 - (SELECT sec FROM elapsed) / $3::float8) AS w
  ),
  bumped AS (
    INSERT INTO api_token_rate_buckets (token_id, bucket_start, count)
    SELECT $1, (SELECT s FROM cur_start), 1
    WHERE 1 + (SELECT w FROM prev_share) <= $2
    ON CONFLICT (token_id, bucket_start) DO UPDATE
      SET count = api_token_rate_buckets.count + 1
      WHERE api_token_rate_buckets.count + 1 + (SELECT w FROM prev_share) <= $2
    RETURNING count
  )
SELECT
  (SELECT count FROM bumped)::bigint AS admitted,
  (SELECT c FROM cur_count) AS current_count,
  (SELECT c FROM prev_count) AS previous_count,
  (SELECT sec FROM elapsed) AS elapsed_seconds;
`

	var (
		admitted            *int64
		curCount, prevCount int64
		elapsedSec          float64
	)
	if err := l.pool.QueryRow(ctx, stmt, tokenID, capacity, ratelimit.Window.Seconds()).
		Scan(&admitted, &curCount, &prevCount, &elapsedSec); err != nil {
		return ratelimit.Decision{}, fmt.Errorf("ratelimit/postgres: bump: %w", err)
	}
	elapsed := time.Duration(elapsedSec * float64(time.Second))
	if admitted != nil {
		return ratelimit.Decision{
			Allowed:       true,
			WeightedCount: ratelimit.Weighted(*admitted, prevCount, elapsed),
		}, nil
	}
	return ratelimit.Decision{
		WeightedCount: ratelimit.Weighted(curCount+1, prevCount, elapsed),
		RetryAfter:    ratelimit.RetryAfter(curCount, prevCount, elapsed, capacity),
	}, nil
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
	return ratelimit.Snapshot{
		CurrentBucketCount:  curCount,
		PreviousBucketCount: prevCount,
		WeightedCount:       ratelimit.Weighted(curCount, prevCount, time.Duration(elapsed*float64(time.Second))),
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
