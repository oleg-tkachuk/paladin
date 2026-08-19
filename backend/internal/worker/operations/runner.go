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

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/operation"
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
}

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
	response, err := exec.Execute(r.withProgress(ctx, op), op)
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
	if err := r.Repo.UpdateState(ctx, op.OperationID,
		operation.StateSucceeded, op.Metadata, response, "", ""); err != nil {
		logger.Warn("failed to mark SUCCEEDED", zap.Error(err))
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
	if err := r.Repo.UpdateState(ctx, op.OperationID,
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
