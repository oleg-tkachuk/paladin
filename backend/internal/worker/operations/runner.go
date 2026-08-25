// Package operations runs the long-running operation queue (BatchDelete /
// BatchCopy / BatchUpdateTags / BatchRestoreObjects, …). Handlers in
// internal/api/v1/batch enqueue rows into the `operations` table; this
// package is what actually picks them up and executes the work.
//
// Architecture: one Runner per pod that loops on a ticker, calls
// ClaimNext atomically (FOR UPDATE SKIP LOCKED), dispatches to a
// type-keyed Executor map, and writes the result back via UpdateState.
// Multiple replicas of the worker pod compete safely — the SKIP LOCKED
// in ClaimNext gives them disjoint rows.
//
// Executors are pluggable: the Runner doesn't know how to delete or
// copy; it just owns the queue lifecycle. Each operation type registers
// its own Executor with the kind of dependencies it actually needs
// (object repo, statemachine, storage client). Unknown types are
// marked FAILED with a UNKNOWN_TYPE error code so a client polling the
// operation gets an unambiguous signal rather than waiting forever.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
)

// Executor is the per-operation-type seam. Implementations parse the
// Operation.Metadata bytes (typically JSON-encoded args) and perform
// the work.
//
// Return contract:
//
//   - response, nil    → operation marked SUCCEEDED with the response
//     bytes attached.
//   - nil, err         → operation marked FAILED with err.Error() in
//     ErrorMessage and a coarse ErrorCode taken
//     from the executor (see ErrCode).
//
// Executors should be safe to interrupt — the parent ctx is cancelled
// when the worker pod shuts down. Long-running iteration loops MUST
// honour ctx.Err() between items.
type Executor interface {
	Execute(ctx context.Context, op operation.Operation) (response []byte, err error)
}

// Runner is the operation-queue consumer. One per worker pod; the
// pod's ServiceAccount + lease infrastructure (internal/worker/lease)
// gates "should I be running" — this struct just owns the loop.
type Runner struct {
	Repo      operation.Repository
	Executors map[string]Executor
	Interval  time.Duration
	Logger    *zap.Logger

	// Heartbeat is how often a RUNNING operation's updated_at is refreshed
	// while its executor works. It is what lets the stale-operation reclaim
	// distinguish a long batch from a worker that died mid-flight. Zero
	// picks a sane default; it must stay well under the reclaim threshold.
	Heartbeat time.Duration

	// Toucher refreshes the heartbeat. Optional: a Repo that does not
	// implement it simply runs without one, and long operations are then
	// only kept alive by their own progress reports.
	Toucher OperationToucher
}

// OperationToucher is the heartbeat seam, kept separate from
// operation.Repository so existing implementations (and tests) that do not
// need it are unaffected.
type OperationToucher interface {
	Touch(ctx context.Context, opID uuid.UUID) error
}

// terminalWriteTimeout bounds the write that records SUCCEEDED or FAILED.
// It runs on a context detached from the runner's, so a shutdown cannot stop
// the row from reaching a terminal state — see runOne.
const terminalWriteTimeout = 10 * time.Second

// Run blocks until ctx is cancelled. The loop:
//   - on every tick, drain as many PENDING operations as ClaimNext
//     can return (so a backlog burns down quickly without waiting for
//     N tick intervals);
//   - on ErrNoOperationToClaim, sleep until the next tick;
//   - on any other claim error, log and back off until the next tick.
//
// Per-operation execution failures (executor returns error) update the
// row to FAILED but do NOT exit the loop; one bad operation must not
// stall the queue.
func (r *Runner) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = 5 * time.Second
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.drain(ctx)
		}
	}
}

// drain claims-and-runs until the queue empties or claim errors. Bounded
// internally to avoid one tick monopolising the worker — at most 100
// operations per tick, then yield to the next ticker fire so other
// timers (housekeeping, lease renewal in the parent worker) get
// scheduled fairly.
func (r *Runner) drain(ctx context.Context) {
	const maxPerTick = 100
	for i := 0; i < maxPerTick; i++ {
		if ctx.Err() != nil {
			return
		}
		op, err := r.Repo.ClaimNext(ctx)
		if errors.Is(err, operation.ErrNoOperationToClaim) {
			return
		}
		if err != nil {
			r.log().Warn("claim failed; will retry next tick", zap.Error(err))
			return
		}
		r.runOne(ctx, op)
	}
}

// runOne dispatches one claimed operation. Always finishes by writing
// a terminal state (SUCCEEDED or FAILED) — leaving an op in RUNNING
// after we've claimed it is the "stuck operation" failure mode that
// users reported in the original BACKLOG entry.
func (r *Runner) runOne(ctx context.Context, op operation.Operation) {
	logger := r.log().With(
		zap.String("operation_id", op.OperationID.String()),
		zap.String("type", op.Type),
		zap.String("tenant_id", op.TenantID.String()),
	)

	exec, ok := r.Executors[op.Type]
	if !ok {
		logger.Warn("no executor registered for operation type")
		r.markFailed(ctx, op, "UNKNOWN_TYPE",
			fmt.Sprintf("no executor registered for operation type %q", op.Type))
		return
	}

	logger.Info("executing operation")
	start := time.Now()
	// Scope the connection to the operation's tenant before the executor
	// touches anything. The worker runs as paladin_app, which has no BYPASSRLS,
	// and objects/collections are under FORCE ROW LEVEL SECURITY — so without
	// this every batch executor read back zero rows and reported each id as
	// "not found", while the operation itself dutifully recorded SUCCEEDED
	// with failed == total. The tenant is not caller-supplied: it comes off
	// the claimed operation row, which the RPC wrote under the caller's own
	// tenant scope.
	execCtx := auth.WithActingTenant(r.withProgress(ctx, op), op.TenantID)
	stopHeartbeat := make(chan struct{})
	go r.heartbeat(ctx, op, stopHeartbeat)
	response, err := exec.Execute(execCtx, op)
	close(stopHeartbeat)
	duration := time.Since(start)

	if err != nil {
		logger.Warn("executor failed",
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		r.markFailed(ctx, op, "EXEC_FAILED", err.Error())
		return
	}

	logger.Info("operation succeeded",
		zap.Duration("duration", duration),
		zap.Int("response_bytes", len(response)),
	)
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if err := r.Repo.UpdateState(writeCtx, op.OperationID,
		operation.StateSucceeded, op.Metadata, response, "", ""); err != nil {
		logger.Warn("failed to mark SUCCEEDED", zap.Error(err))
	}
}

// terminalCtx detaches the terminal write from the runner's lifecycle.
//
// Both terminal writes used to run on the runner's own ctx. On shutdown that
// ctx is already cancelled by the time the executor returns — it is what
// cancelled the executor — so the UPDATE was refused before it reached
// Postgres, the failure was logged as a warning, and the row stayed RUNNING.
// Nothing revisits a RUNNING row: ClaimNext takes only PENDING and the reaper
// only terminal states, so it stayed that way indefinitely. One such row sat
// RUNNING for two days while an identical operation, retried nine minutes
// later, finished in 2.5 seconds.
//
// WithoutCancel keeps the values (trace, logger) and drops the cancellation;
// the timeout stops a shutdown from being held open by a stuck database.
func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

// heartbeat refreshes the operation's updated_at until stop is closed. See
// Runner.Heartbeat for why liveness cannot be left to progress reporting.
func (r *Runner) heartbeat(ctx context.Context, op operation.Operation, stop <-chan struct{}) {
	if r.Toucher == nil {
		return
	}
	every := r.Heartbeat
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Toucher.Touch(ctx, op.OperationID); err != nil {
				r.log().Debug("operation heartbeat failed",
					zap.String("operation_id", op.OperationID.String()), zap.Error(err))
			}
		}
	}
}

// withProgress installs a throttled progress reporter on ctx. Executors call
// operations.ReportProgress in their loops; this writes `{processed, total}`
// to the operation row's metadata (state RUNNING) so a polling client renders
// a live progress bar. Writes are throttled to ~1/s (the final tick always
// lands) to keep the queue's DB pressure bounded on large batches. The
// terminal success/fail write in runOne / markFailed is authoritative.
func (r *Runner) withProgress(ctx context.Context, op operation.Operation) context.Context {
	var last time.Time
	return WithProgress(ctx, func(processed, total int) {
		now := time.Now()
		if processed < total && now.Sub(last) < time.Second {
			return
		}
		last = now
		meta, _ := json.Marshal(struct {
			Processed int `json:"processed"`
			Total     int `json:"total"`
		}{processed, total})
		if err := r.Repo.UpdateState(ctx, op.OperationID,
			operation.StateRunning, meta, nil, "", ""); err != nil {
			r.log().Debug("progress write failed", zap.Error(err))
		}
	})
}

// markFailed encodes the error to a JSON-friendly response field too
// so polling clients can render the error without a separate fetch.
func (r *Runner) markFailed(ctx context.Context, op operation.Operation, code, msg string) {
	resp, _ := json.Marshal(map[string]string{"error": msg, "code": code})
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if err := r.Repo.UpdateState(writeCtx, op.OperationID,
		operation.StateFailed, op.Metadata, resp, code, msg); err != nil {
		r.log().Warn("failed to mark FAILED", zap.Error(err))
	}
}

func (r *Runner) log() *zap.Logger {
	if r.Logger == nil {
		return zap.NewNop()
	}
	return r.Logger
}
