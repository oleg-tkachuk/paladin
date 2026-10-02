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
// "already-owned-by-you", and each state write applies only to a row still in
// the state the worker listed it in, so a deletion requested mid-provision is
// not undone and a bucket created again under the same name is not deleted.

package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// BucketProvisioner is the subset of bucketh.Provisioner the reconciler
// uses. Re-declared here to avoid an import cycle (bucketh would otherwise
// pull worker for tests). The seam is small enough that duplication is
// cheaper than introducing a shared package.
type BucketProvisioner interface {
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
	// TagBucketOwner tags a dedicated bucket with its owner tenant_id for cost
	// attribution (ADR-0015). Best-effort at the call site.
	TagBucketOwner(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error
}

// BucketProvisionRepo is the subset of admindomain.BucketRepository the
// reconciler needs. *adapters.BucketRepoV2 satisfies it.
type BucketProvisionRepo interface {
	ListPendingProvisions(ctx context.Context, maxAttempts, limit int32) ([]admindomain.BucketProvisionRow, error)
	MarkProvisionReady(ctx context.Context, backendID, bucketName string) error
	MarkProvisionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error

	// Delete-side outbox.
	ListPendingDeletions(ctx context.Context, maxAttempts, limit int32) ([]admindomain.BucketProvisionRow, error)
	// Terminal removal + paladin.bucket.deleted, atomic (ADR-0003), on one
	// tx via RunInTx. GetTx resolves the state and the owner tenant_id (the
	// fan-out target lives only on the row); DeleteTx removes the row at the
	// version GetTx read; the closure enqueues the event.
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	// LockTx row-locks the bucket for the rest of tx, so the state read after
	// it is the state the backend delete runs under. ErrNotFound when gone.
	LockTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) error
	GetTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) (admindomain.Bucket, error)
	DeleteTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, expectedVersion int64) error
	MarkDeletionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error
}

// BucketEventProducer is the outbox fan-out seam the reconciler uses to
// enqueue the terminal paladin.bucket.deleted on the row-removal tx. nil-safe:
// an unwired reconciler just removes the row. *Dispatcher implements it.
type BucketEventProducer interface {
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt Event) (int, error)
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
	repo   BucketProvisionRepo
	prov   BucketProvisioner
	cfg    BucketReconcilerConfig
	events BucketEventProducer // nil-safe
	log    *zap.Logger
}

// SetEventProducer attaches the optional outbox producer so the terminal
// row removal also enqueues paladin.bucket.deleted in the same tx (ADR-0003).
// nil-safe / opt-in — same contract as the handlers' SetEventProducer.
func (r *BucketReconciler) SetEventProducer(p BucketEventProducer) { r.events = p }

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
	// Kick once on startup so a fresh restart doesn't leave a row
	// languishing for a full Interval before the first attempt.
	r.tick(ctx)
	return RunTicker(ctx, "bucket_reconciler", r.cfg.Interval, func(ctx context.Context) error {
		r.tick(ctx)
		return nil
	})
}

func (r *BucketReconciler) tick(ctx context.Context) {
	// Create-side scan.
	rows, err := r.repo.ListPendingProvisions(ctx, r.cfg.MaxAttempts, r.cfg.BatchSize)
	if err != nil {
		r.log.Warn("list pending bucket provisions failed", zap.Error(err))
	} else {
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return
			}
			r.reconcileOne(ctx, row)
		}
	}

	// Delete-side scan. Same batch budget — operationally a backlog of
	// 'deleting' rows is no more urgent than a backlog of 'pending'
	// ones; both block reconvergence equally.
	delRows, err := r.repo.ListPendingDeletions(ctx, r.cfg.MaxAttempts, r.cfg.BatchSize)
	if err != nil {
		r.log.Warn("list pending bucket deletions failed", zap.Error(err))
		return
	}
	for _, row := range delRows {
		if err := ctx.Err(); err != nil {
			return
		}
		r.reconcileDeleteOne(ctx, row)
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
		// Cost attribution (ADR-0015): tag a dedicated (owned) bucket with its
		// tenant_id so cloud cost reports group bucket→tenant. Best-effort —
		// a backend that doesn't support PutBucketTagging must not block
		// provisioning; the bucket is already usable.
		if row.OwnerTenantID != uuid.Nil {
			if tErr := r.prov.TagBucketOwner(ctx, row.BackendID, row.BucketName, row.OwnerTenantID); tErr != nil {
				log.Warn("bucket cost-attribution tagging failed (non-fatal)", zap.Error(tErr))
			}
		}
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

// reconcileDeleteOne is the delete-path twin of reconcileOne. On one tx,
// with the row locked: re-check it is still marked for deletion, delete the
// backend bucket, then the row. Both deletes are idempotent: the backend
// swallows NoSuchBucket, and a row already gone is a replica that finished.
func (r *BucketReconciler) reconcileDeleteOne(ctx context.Context, row admindomain.BucketProvisionRow) {
	log := r.log.With(
		zap.String("backend_id", row.BackendID),
		zap.String("bucket_name", row.BucketName),
		zap.Int32("attempt", row.ProvisionAttempts+1),
	)

	if r.prov == nil {
		// No backend wired — see reconcileOne's note. Mark transient so
		// a future deploy can converge.
		_ = r.repo.MarkDeletionFailed(ctx, row.BackendID, row.BucketName,
			false, "no provisioner wired")
		log.Debug("no provisioner wired, leaving row deleting")
		return
	}

	// The backend delete runs under the row lock, after re-reading the state:
	// a bucket deleted and created again under the same name since it was
	// listed is a new bucket, and its backend bucket is not ours to remove.
	// The row removal and paladin.bucket.deleted share the same tx (ADR-0003).
	var backendErr error
	deleted := false
	if err := r.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// A missing row means a concurrent replica already finished — treat
		// as done, no event.
		if lErr := r.repo.LockTx(ctx, tx, row.BackendID, row.BucketName); errors.Is(lErr, admindomain.ErrNotFound) {
			return nil
		} else if lErr != nil {
			return lErr
		}
		b, gErr := r.repo.GetTx(ctx, tx, row.BackendID, row.BucketName)
		if errors.Is(gErr, admindomain.ErrNotFound) {
			return nil
		}
		if gErr != nil {
			return gErr
		}
		if !markedForDeletion(b.ProvisionState) {
			log.Warn("bucket is no longer marked for deletion; left alone",
				zap.String("provision_state", b.ProvisionState))
			return nil
		}
		if err := r.prov.DeleteBucket(ctx, row.BackendID, row.BucketName); err != nil {
			backendErr = err
			return nil
		}
		// At the version read on this tx, so a concurrent change refuses the
		// delete rather than being removed with it.
		if dErr := r.repo.DeleteTx(ctx, tx, row.BackendID, row.BucketName, b.ResourceVersion); dErr != nil {
			return dErr
		}
		deleted = true
		return r.emitBucketDeleted(ctx, tx, b)
	}); err != nil {
		log.Warn("bucket delete transaction failed", zap.Error(err))
		return
	}
	if backendErr != nil {
		terminal := isTerminalDeletionError(backendErr)
		if mErr := r.repo.MarkDeletionFailed(ctx, row.BackendID, row.BucketName, terminal, backendErr.Error()); mErr != nil {
			log.Warn("mark-deletion-failed write failed",
				zap.Bool("terminal", terminal), zap.Error(mErr), zap.NamedError("backend_err", backendErr))
			return
		}
		if terminal {
			log.Error("bucket deletion failed permanently", zap.Error(backendErr))
		} else {
			log.Warn("bucket deletion failed (transient)", zap.Error(backendErr))
		}
		return
	}
	if !deleted {
		return
	}
	log.Info("bucket deleted")
}

// markedForDeletion reports whether a row is in the deletion outbox.
func markedForDeletion(state string) bool {
	return state == admindomain.BucketProvisionStateDeleting ||
		state == admindomain.BucketProvisionStateDeletionFailed
}

// emitBucketDeleted enqueues the terminal paladin.bucket.deleted on the row-
// removal tx. nil-safe (no producer → no-op). Mirrors the handler's
// immediate-delete payload; mode "outbox" marks the async completion.
func (r *BucketReconciler) emitBucketDeleted(ctx context.Context, tx pgx.Tx, b admindomain.Bucket) error {
	if r.events == nil {
		return nil
	}
	_, err := r.events.DispatchTx(ctx, tx, b.OwnerTenantID.String(), Event{
		Type:         "paladin.bucket.deleted",
		At:           time.Now().UTC(),
		TenantID:     b.OwnerTenantID.String(),
		ResourceName: fmt.Sprintf("tenants/%s/buckets/%s/%s", b.OwnerTenantID, b.BackendID, b.BucketName),
		Payload: map[string]any{
			"tenant_id":   b.OwnerTenantID.String(),
			"backend_id":  b.BackendID,
			"bucket_name": b.BucketName,
			"mode":        "outbox",
		},
	})
	return err
}

// isTerminalDeletionError mirrors isTerminalProvisionError for the
// delete side. BucketNotEmpty is the canonical non-retryable here —
// the caller has to remove the contents first; retrying without that
// will keep failing.
func isTerminalDeletionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"AccessDenied",
		"BucketNotEmpty",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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
