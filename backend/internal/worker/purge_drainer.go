package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// PurgeDrainer reclaims bytes whose database row is already gone.
//
// The permanent-delete path deletes the objects row first and the S3 bytes
// second — deliberately, because the reverse ordering could delete a live
// object's bytes and then fail to remove its row. The residue of the safe
// ordering is bytes with no row, and until this worker existed that residue
// was unrecoverable: nothing in the schema remembered where those bytes were,
// so the only way to find one was to list the bucket.
//
// The handler now writes a pending_purges row in the same transaction that
// deletes the object, then attempts the byte-delete itself. This drains
// whatever that attempt could not finish. It is the retry half of a
// transactional outbox over bytes (ADR-0003), and it is the component that
// emits paladin.object.purged — the event that means the bytes are actually gone,
// as distinct from paladin.object.deleted, which only means the row changed state.
//
// Idempotent throughout: S3 DELETE on an absent key succeeds, so re-issuing a
// delete that already landed is free. That is what makes "leave the debt row
// on any doubt" the correct failure policy.
type PurgeDrainer struct {
	Pool    *pgxpool.Pool
	Q       *sqlc.Queries
	Storage StorageDeleter
	// Events is optional. Without it the bytes are still reclaimed and the
	// debt still settles — only paladin.object.purged is skipped, which keeps a
	// deployment with no subscriptions from needing a dispatcher here.
	Events    *Dispatcher
	Interval  time.Duration
	BatchSize int32
	// MaxBackoff caps the retry curve. Debt is never dropped: a row that
	// keeps failing keeps being retried at this cadence, because the
	// alternative — discarding it — is silently leaking the bytes again,
	// which is the bug this worker exists to fix.
	MaxBackoff time.Duration
	Logger     *zap.Logger
}

// Run blocks until ctx is cancelled. Disabled when Interval <= 0 — appropriate
// only for a deployment where permanent delete is never used, since otherwise
// a failed byte-delete has no other path to completion.
func (w *PurgeDrainer) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		return nil
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 100
	}
	if w.MaxBackoff <= 0 {
		w.MaxBackoff = time.Hour
	}
	return RunTicker(ctx, "purge_drainer", w.Interval, func(ctx context.Context) error {
		w.Sweep(ctx)
		return nil
	})
}

// Sweep drains one batch of due debt.
//
// Exported so a test can drive a single tick without the ticker.
//
// The claim runs in its own transaction with FOR UPDATE SKIP LOCKED, so
// concurrent worker replicas split the backlog rather than racing for the same
// rows. The storage calls happen INSIDE that transaction's lifetime on
// purpose: holding the row lock for the duration is what stops a second
// replica from issuing a duplicate delete while this one is mid-flight. The
// batch is small and the lock is per-row, so the contention cost is bounded.
func (w *PurgeDrainer) Sweep(ctx context.Context) {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		w.log().Warn("purge drainer: begin", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := w.Q.WithTx(tx).ListDuePurges(ctx, w.BatchSize)
	if err != nil {
		w.log().Warn("purge drainer: list due", zap.Error(err))
		return
	}
	// A row whose settlement must not stand aborts the whole tick.
	//
	// drainOne used to return nothing, and its `return` on a failed
	// announcement exited drainOne — not Sweep — so the commit below ran
	// anyway and persisted the DeletePendingPurge that had already executed
	// in this transaction. The debt settled, the event was never emitted, and
	// the row was gone, so no later tick could retry it. The comment there
	// said the tick rolls back; nothing did.
	rollback := false
	for _, r := range rows {
		if ctx.Err() != nil {
			return
		}
		if err := w.drainOne(ctx, tx, r); err != nil {
			rollback = true
			break
		}
	}
	if rollback {
		// The deferred Rollback does the work. Every settlement in this tick
		// is discarded, including ones that succeeded — which is the trade the
		// emit branch already chose: an unannounced purge is worse than a
		// repeated one, and the repeat is free because the storage DELETE is
		// idempotent.
		return
	}
	if err := tx.Commit(ctx); err != nil {
		// Nothing is lost: uncommitted settlements mean the debt rows stay,
		// and the next tick re-issues storage deletes that are idempotent.
		w.log().Warn("purge drainer: commit", zap.Error(err))
	}
}

// drainOne reclaims one object's bytes and settles its debt.
//
// Order is bytes-then-bookkeeping, which is safe here for the same reason it
// is safe in LifecycleHardDeleter and NOT safe in the API handler: there is no
// live row to strand. The row this debt describes was deleted before the debt
// was ever written.
// drainOne reclaims one object's bytes and settles its debt.
//
// A non-nil error means THIS TICK MUST NOT COMMIT: the row's settlement is
// already written to the transaction and standing it up without the
// announcement is the failure the emit branch exists to prevent. A storage
// failure is not one of those — it records attempts and last_error, which are
// writes that MUST survive, so it returns nil.
func (w *PurgeDrainer) drainOne(ctx context.Context, tx pgx.Tx, r sqlc.ListDuePurgesRow) error {
	purgeID := uuid.UUID(r.ID.Bytes)
	tenantID := uuid.UUID(r.TenantID.Bytes)
	logger := w.log().With(
		zap.String("purge_id", purgeID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("bucket", r.BucketName),
		zap.String("path", r.Path),
		zap.Int32("attempts", r.Attempts),
	)

	if err := w.Storage.DeleteObject(ctx, r.BackendName, r.BucketName, tenantID, r.CollectionName, r.Path); err != nil {
		backoff := w.backoffFor(r.Attempts)
		if rerr := w.Q.WithTx(tx).ReschedulePendingPurge(ctx, r.ID, ptrTo(err.Error()),
			pgtype.Interval{Microseconds: backoff.Microseconds(), Valid: true}); rerr != nil {
			// nil, not rerr: nothing was settled for this row, so the tick
			// may still commit whatever the other rows achieved.
			logger.Warn("purge drainer: reschedule failed", zap.Error(rerr))
			return nil
		}
		logger.Warn("purge drainer: storage delete failed; will retry",
			zap.Duration("retry_in", backoff), zap.Error(err))
		// nil on purpose: the attempts/last_error write above MUST survive,
		// and the debt row is untouched.
		return nil
	}

	// Settle and announce on the same tx. paladin.object.purged is emitted ONLY
	// here and on the equivalent path in LifecycleHardDeleter — never on a
	// state transition — so a consumer that sees it can rely on the bytes
	// being gone.
	if _, err := w.Q.WithTx(tx).DeletePendingPurge(ctx, r.ID); err != nil {
		logger.Warn("purge drainer: settle failed", zap.Error(err))
		return err
	}
	if err := w.emitPurged(ctx, tx, tenantID, r); err != nil {
		// Roll the whole tick back rather than settle silently: an
		// unannounced purge is worse than a repeated one, and the repeat is
		// free (idempotent DELETE).
		logger.Warn("purge drainer: emit paladin.object.purged failed", zap.Error(err))
		return err
	}
	logger.Info("purged")
	return nil
}

// emitPurged enqueues paladin.object.purged on the caller's tx. Shape mirrors
// paladin.tenant.purged, the existing precedent for "the thing is really gone".
func (w *PurgeDrainer) emitPurged(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, r sqlc.ListDuePurgesRow) error {
	if w.Events == nil {
		return nil
	}
	objectID := uuid.UUID(r.ObjectID.Bytes)
	_, err := w.Events.DispatchTx(ctx, tx, tenantID.String(), Event{
		Type:     "paladin.object.purged",
		At:       time.Now().UTC(),
		TenantID: tenantID.String(),
		ResourceName: fmt.Sprintf("storageBackends/%s/buckets/%s/tenants/%s/collections/%s/objects-by-key/%s",
			r.BackendName, r.BucketName, tenantID, r.CollectionName, r.Path),
		Payload: map[string]any{
			"tenant_id":  tenantID.String(),
			"collection": r.CollectionName,
			"key":        r.Path,
			"object_id":  objectID.String(),
			"backend_id": r.BackendName,
			"bucket":     r.BucketName,
			"reclaimed":  true,
		},
	})
	return err
}

// backoffFor doubles per attempt from one minute, capped at MaxBackoff. Slow
// on purpose: a persistent failure here is a backend or credential problem an
// operator has to fix, and hammering it every tick only fills the log.
func (w *PurgeDrainer) backoffFor(attempts int32) time.Duration {
	d := time.Minute
	for i := int32(0); i < attempts && d < w.MaxBackoff; i++ {
		d *= 2
	}
	if d > w.MaxBackoff {
		d = w.MaxBackoff
	}
	return d
}

func (w *PurgeDrainer) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}

func ptrTo[T any](v T) *T { return &v }
