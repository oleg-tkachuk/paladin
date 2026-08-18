package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// QuotaUsageStore is the slice of the quota repository this worker needs.
// Narrow on purpose: the job's logic is "call these two, log, keep going",
// and a fake in the unit test shouldn't have to satisfy the full
// admindomain.QuotaRepository.
type QuotaUsageStore interface {
	// ReconcileUsage recomputes stored-usage columns from live objects and
	// returns the number of rows that were actually wrong.
	ReconcileUsage(ctx context.Context) (int64, error)
	// RollDailyCounters zeroes per-day admission counters last reset before
	// dayStart, returning the number of rows rolled.
	RollDailyCounters(ctx context.Context, dayStart time.Time) (int64, error)
}

// QuotaReconciler keeps the `quotas` usage columns honest.
//
// The upload path maintains them incrementally and best-effort: object and
// multipart handlers call OnObjectPromoted after a successful promote and
// deliberately swallow its error, because a pgx blip must not undo a
// committed state transition. Nothing decremented them on delete, and
// nothing recomputed them — so the counters could only climb.
//
// That is not cosmetic. middleware.QuotaSoftCheck rejects uploads with
// ResourceExhausted by comparing those columns against max_total_bytes /
// max_object_count, so an unreconciled deployment eventually wedges a
// tenant that has deleted everything it uploaded. This job closes the
// loop from the other end: periodically recompute from the rows that are
// the actual source of truth.
//
// Two independent halves per tick, both attempted even if one fails:
//
//   - stored usage (total bytes / object count) — recomputed from live
//     objects, so it is self-healing regardless of how many increments the
//     write path dropped.
//   - per-day admission counters — rolled at the UTC day boundary. These
//     stay incremental by design (see the adapter's rollDailySQL comment),
//     and without the roll a per-day cap silently becomes a lifetime one.
//
// Disabled when Interval <= 0. Runs under a lease like every other
// background job, so exactly one pod reconciles at a time.
type QuotaReconciler struct {
	Store    QuotaUsageStore
	Interval time.Duration
	Logger   *zap.Logger
}

func (q *QuotaReconciler) Run(ctx context.Context) error {
	if q.Interval <= 0 {
		return nil
	}
	return RunTicker(ctx, "quota_reconciler", q.Interval, func(ctx context.Context) error {
		var tickErr error

		// Corrections are logged at Info, not Debug: a steady-state fleet
		// reconciles zero rows, so any non-zero count is a signal that the
		// increment path is losing writes and is worth seeing without
		// turning up log levels.
		if n, err := q.Store.ReconcileUsage(ctx); err != nil {
			q.log().Warn("failed to reconcile quota usage", zap.Error(err))
			tickErr = err
		} else if n > 0 {
			q.log().Info("corrected drifted quota usage", zap.Int64("rows", n))
		}

		// Truncating to the UTC day is what makes the roll idempotent: every
		// tick within the same day computes the same boundary, so the guard
		// `last_reset_at < dayStart` matches only rows that have not been
		// rolled today — whichever tick gets there first does the work.
		dayStart := time.Now().UTC().Truncate(24 * time.Hour)
		if n, err := q.Store.RollDailyCounters(ctx, dayStart); err != nil {
			q.log().Warn("failed to roll daily quota counters", zap.Error(err))
			if tickErr == nil {
				tickErr = err
			}
		} else if n > 0 {
			q.log().Info("rolled daily quota counters",
				zap.Int64("rows", n), zap.Time("day_start", dayStart))
		}

		return tickErr
	})
}

func (q *QuotaReconciler) log() *zap.Logger {
	if q.Logger == nil {
		return zap.NewNop()
	}
	return q.Logger
}
