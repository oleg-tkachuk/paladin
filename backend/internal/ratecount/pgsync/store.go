package pgsync

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Batcher sends a batch in one round trip; *pgxpool.Pool is one.
type Batcher interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// PostgresStore shares counts in rate_limit_buckets.
type PostgresStore struct{ db Batcher }

// NewPostgresStore shares through db.
func NewPostgresStore(db Batcher) *PostgresStore { return &PostgresStore{db: db} }

const (
	sqlAddDeltas = `
INSERT INTO rate_limit_buckets (key, bucket_start, count)
SELECT * FROM unnest($1::text[], $2::timestamptz[], $3::bigint[])
ON CONFLICT (key, bucket_start) DO UPDATE SET count = rate_limit_buckets.count + EXCLUDED.count`

	sqlReadTotals = `
SELECT key, bucket_start, count FROM rate_limit_buckets
WHERE key = ANY ($1::text[]) AND bucket_start >= $2`

	sqlSweep = `DELETE FROM rate_limit_buckets WHERE bucket_start < $1`
)

// Exchange implements Store: the deltas and the read-back go in one round
// trip, the read after the write, so the totals include this sync's counts.
func (s *PostgresStore) Exchange(ctx context.Context, deltas []Delta, keys []string, since time.Time) ([]Total, error) {
	b := &pgx.Batch{}
	if len(deltas) > 0 {
		dk, ds, dc := make([]string, len(deltas)), make([]time.Time, len(deltas)), make([]int64, len(deltas))
		for i, d := range deltas {
			dk[i], ds[i], dc[i] = d.Key, d.Start, d.Count
		}
		b.Queue(sqlAddDeltas, dk, ds, dc)
	}
	b.Queue(sqlReadTotals, keys, since)
	res := s.db.SendBatch(ctx, b)
	defer func() { _ = res.Close() }()
	if len(deltas) > 0 {
		if _, err := res.Exec(); err != nil {
			return nil, fmt.Errorf("pgsync: add deltas: %w", err)
		}
	}
	rows, err := res.Query()
	if err != nil {
		return nil, fmt.Errorf("pgsync: read totals: %w", err)
	}
	totals, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Total, error) {
		var t Total
		err := r.Scan(&t.Key, &t.Start, &t.Count)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("pgsync: read totals: %w", err)
	}
	return totals, nil
}

// Sweeper drops buckets that have left every window.
type Sweeper struct{ db Execer }

// Execer runs one statement; *pgxpool.Pool is one.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// NewSweeper sweeps through db.
func NewSweeper(db Execer) *Sweeper { return &Sweeper{db: db} }

// SweepRateLimitBuckets deletes buckets that started before olderThan ago.
func (s *Sweeper) SweepRateLimitBuckets(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.db.Exec(ctx, sqlSweep, time.Now().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("pgsync: sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}
