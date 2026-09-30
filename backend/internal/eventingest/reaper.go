package eventingest

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Reaper drops ingested_events rows older than TTL. Runs alongside
// the ingest worker (or in any pod running BackgroundJobs) so the
// dedup table stays bounded.
//
// Sweep semantics: PurgeIngestedEventsBefore deletes 10k rows at a
// time and returns rowsAffected. We loop until 0 to drain a backlog
// without holding a single statement open for minutes.
type Reaper struct {
	Q        *sqlc.Queries
	Interval time.Duration
	TTL      time.Duration
	Logger   *zap.Logger
}

func (r *Reaper) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = time.Hour
	}
	if r.TTL <= 0 {
		r.TTL = 24 * time.Hour
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.sweep(ctx)
		}
	}
}

func (r *Reaper) sweep(ctx context.Context) {
	cutoff := time.Now().Add(-r.TTL)
	ts := pgtype.Timestamptz{Time: cutoff, Valid: true}
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := r.Q.PurgeIngestedEventsBefore(ctx, ts)
		if err != nil {
			r.log().Warn("ingested_events purge", zap.Error(err))
			return
		}
		if n == 0 {
			return
		}
	}
}

func (r *Reaper) log() *zap.Logger {
	if r.Logger == nil {
		return zap.NewNop()
	}
	return r.Logger
}
