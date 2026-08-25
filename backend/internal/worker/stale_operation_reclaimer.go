package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// StaleOperationReclaimer fails operations abandoned by a worker that stopped
// between claiming one and writing its result.
//
// Such a row is invisible to everything else in the system: ClaimNext selects
// only PENDING so no worker retries it, and OperationsReaper deletes only
// terminal states so it is never even cleaned up. It shows in the console as
// running, indefinitely — one sat that way for two days while the identical
// operation, retried nine minutes later, finished in 2.5 seconds.
//
// The runner's terminal write is now detached from its shutdown context,
// which closes the common case (a graceful rollout mid-operation). This
// covers what that cannot: SIGKILL, a node loss, a database outage spanning
// the write's timeout.
type StaleOperationReclaimer struct {
	Repo     StaleOperationRepo
	Interval time.Duration
	// StaleAfter is how long a RUNNING operation may go without a heartbeat
	// before it is declared lost. The runner heartbeats every 30s, so this
	// wants to be several multiples of that: too tight and a live operation
	// is failed under a slow database, too loose and the console shows a dead
	// one as running for that much longer. Zero picks the default.
	StaleAfter time.Duration
	Logger     *zap.Logger
}

// StaleOperationRepo is the narrow seam — one method, mirroring the shape
// OperationsReaper uses.
type StaleOperationRepo interface {
	ReclaimStale(ctx context.Context, staleAfter time.Duration) (int64, error)
}

// Run ticks until ctx is done. A zero Interval disables the job.
func (w *StaleOperationReclaimer) Run(ctx context.Context) error {
	if w.Interval <= 0 || w.Repo == nil {
		return nil
	}
	if w.StaleAfter <= 0 {
		w.StaleAfter = 15 * time.Minute
	}
	return RunTicker(ctx, "stale_operation_reclaimer", w.Interval, func(ctx context.Context) error {
		n, err := w.Repo.ReclaimStale(ctx, w.StaleAfter)
		if err != nil {
			w.log().Warn("failed to reclaim stale operations", zap.Error(err))
			return err
		}
		if n > 0 {
			// Info, not Debug: every row here is an operation whose caller was
			// told "running" and will now be told "failed, outcome unknown".
			w.log().Info("reclaimed operations abandoned by a stopped worker",
				zap.Int64("rows", n),
				zap.Duration("stale_after", w.StaleAfter))
		}
		return nil
	})
}

func (w *StaleOperationReclaimer) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}
