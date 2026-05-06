// BucketReconciler drives the outbox half of BucketService.CreateBucket:
// the handler writes a row in state='pending', this worker calls the
// backend's CreateBucket idempotently, then transitions the row to
// 'ready' (or 'failed' after a retry budget is exhausted).
//
// Why a worker and not an inline call? Two reasons:
//
//   1. Crash safety. Inline "S3 first, DB row second" has a window where
//      the backend bucket exists with no DB record (orphan). Inline "DB
//      row first, then S3" has the inverse: the row claims a ready bucket
//      that the lifecycle/replication/quota workers can't touch. Outbox
//      collapses both into a single source of truth — the row — and lets
//      the worker retry the side effect until convergence.
//
//   2. Backpressure. A flood of CreateBucket RPCs against a slow backend
//      no longer blocks request goroutines. The handler returns as soon
//      as the row is committed; the worker absorbs the throughput.
//
// All actions are idempotent: backend CreateBucket already swallows
// "already-owned-by-you", and MarkProvisionReady is a state-guarded
// UPDATE that is safe to call concurrently from multiple replicas.

package worker

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// BucketProvisioner is the subset of bucketh.Provisioner the reconciler
// uses. Re-declared here to avoid an import cycle (bucketh would otherwise
// pull worker for tests). The seam is small enough that duplication is
// cheaper than introducing a shared package.
type BucketProvisioner interface {
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
}

// BucketProvisionRepo is the subset of admindomain.BucketRepository the
// reconciler needs. *adapters.BucketRepoV2 satisfies it.
type BucketProvisionRepo interface {
	ListPendingProvisions(ctx context.Context, maxAttempts, limit int32) ([]admindomain.BucketProvisionRow, error)
	MarkProvisionReady(ctx context.Context, backendID, bucketName string) error
	MarkProvisionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error
}

type BucketReconcilerConfig struct {
	// Interval is the polling cadence between ticks. 0 → 30s default.
	Interval time.Duration
	// BatchSize caps how many rows a single tick processes. 0 → 25.
	BatchSize int32
	// MaxAttempts is the per-row retry budget; once exceeded, the row is
	// frozen in 'failed' and the worker stops touching it. 0 → 10.
	MaxAttempts int32
}

type BucketReconciler struct {
	repo BucketProvisionRepo
	prov BucketProvisioner
	cfg  BucketReconcilerConfig
	log  *zap.Logger
}

// NewBucketReconciler wires the worker with sensible defaults. The
// caller passes the same Provisioner used by the handler so behavior
// is identical between inline (legacy callers, if any) and async paths.
func NewBucketReconciler(
	repo BucketProvisionRepo,
	prov BucketProvisioner,
	cfg BucketReconcilerConfig,
	log *zap.Logger,
) *BucketReconciler {
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 25
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 10
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &BucketReconciler{repo: repo, prov: prov, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled. Errors from individual rows are
// logged and recorded on the row itself; the loop never aborts on a
// per-row failure.
func (r *BucketReconciler) Run(ctx context.Context) error {
	t := time.NewTicker(r.cfg.Interval)
	defer t.Stop()
	// Kick once on startup so a fresh restart doesn't leave a row
	// languishing for a full Interval before the first attempt.
	r.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *BucketReconciler) tick(ctx context.Context) {
	rows, err := r.repo.ListPendingProvisions(ctx, r.cfg.MaxAttempts, r.cfg.BatchSize)
	if err != nil {
		r.log.Warn("list pending bucket provisions failed", zap.Error(err))
		return
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return
		}
		r.reconcileOne(ctx, row)
	}
}

func (r *BucketReconciler) reconcileOne(ctx context.Context, row admindomain.BucketProvisionRow) {
	log := r.log.With(
		zap.String("backend_id", row.BackendID),
		zap.String("bucket_name", row.BucketName),
		zap.Int32("attempt", row.ProvisionAttempts+1),
	)

	if r.prov == nil {
		// No provisioner wired in this process (e.g. dev mode without an
		// S3 endpoint). Mark transient so a future deploy with a wired
		// provisioner picks it up — don't burn the retry budget.
		_ = r.repo.MarkProvisionFailed(ctx, row.BackendID, row.BucketName,
			false, "no provisioner wired")
		log.Debug("no provisioner wired, leaving row pending")
		return
	}

	err := r.prov.CreateBucket(ctx, row.BackendID, row.BucketName, row.Region)
	if err == nil {
		if mErr := r.repo.MarkProvisionReady(ctx, row.BackendID, row.BucketName); mErr != nil {
			// Backend created the bucket but the DB-side mark failed.
			// Next tick will see the row still pending; the backend
			// CreateBucket call is idempotent (swallows already-owned),
			// so we'll converge.
			log.Warn("backend created but mark-ready failed", zap.Error(mErr))
			return
		}
		log.Info("bucket provisioned")
		return
	}

	terminal := isTerminalProvisionError(err)
	if mErr := r.repo.MarkProvisionFailed(ctx, row.BackendID, row.BucketName, terminal, err.Error()); mErr != nil {
		log.Warn("mark-failed write failed",
			zap.Bool("terminal", terminal), zap.Error(mErr), zap.NamedError("backend_err", err))
		return
	}
	if terminal {
		log.Error("bucket provisioning failed permanently", zap.Error(err))
	} else {
		log.Warn("bucket provisioning failed (transient)", zap.Error(err))
	}
}

// isTerminalProvisionError classifies a backend error as retryable vs
// not. The list mirrors S3 errors that won't change between retries
// without operator action: invalid name, illegal region, denied access,
// quota exceeded. Anything else is treated as transient (network blip,
// throttling, 5xx).
//
// We match on the error string because the AWS SDK wraps backend errors
// in a generic operation error and the caller layer (s3adapter) loses
// the typed code by returning fmt.Errorf wrapped strings. This is
// fragile-but-pragmatic; the worst case is a transient classification
// that triggers a few extra retries before the budget is exhausted.
func isTerminalProvisionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Caller-side cancel / timeout — definitely not the bucket's fault.
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"AccessDenied",
		"InvalidBucketName",
		"InvalidLocationConstraint",
		"IllegalLocationConstraintException",
		"TooManyBuckets",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
