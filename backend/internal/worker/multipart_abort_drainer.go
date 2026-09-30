package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// MultipartAbortDrainer discharges the abort debt recorded by the trigger on
// multipart_uploads (migration 010).
//
// A multipart session that disappears without anyone telling S3 leaves the
// upload open forever, accruing part-storage charges. Two cascades could do
// that — from objects on a permanent delete, and from tenants on a hard
// delete — and neither can call the storage backend, because a foreign key
// cascade runs inside the database. The trigger turns each such deletion into
// a debt row; this worker pays it.
//
// Same contract as PurgeDrainer, whose shape this follows deliberately: the
// debt is never dropped, only retried, because nothing else in the system
// remembers those parts exist. An abort that has already landed is free to
// repeat — S3 treats aborting an unknown upload as success — which is what
// makes "keep the row on any doubt" the correct failure policy.
// MultipartAbortDrainer is the retry half of the multipart-abort obligation.
type MultipartAbortDrainer struct {
	Pool    *pgxpool.Pool
	Q       *sqlc.Queries
	Storage MultipartAborter

	Interval  time.Duration
	BatchSize int32
	// MaxBackoff caps the retry curve; the debt itself never expires.
	MaxBackoff time.Duration
	Logger     *zap.Logger
}

// Run blocks until ctx is cancelled. Interval <= 0 disables it, which is only
// appropriate where multipart upload is unreachable — otherwise a cascade has
// no other path to closing the S3 session.
func (w *MultipartAbortDrainer) Run(ctx context.Context) error {
	if w.Interval <= 0 || w.Pool == nil {
		return nil
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 100
	}
	if w.MaxBackoff <= 0 {
		w.MaxBackoff = time.Hour
	}
	return RunTicker(ctx, "multipart_abort_drainer", w.Interval, func(ctx context.Context) error {
		w.Sweep(ctx)
		return nil
	})
}

// Sweep drains one batch of due debt. Exported so a test can drive a single
// tick without the ticker.
//
// The claim holds FOR UPDATE SKIP LOCKED for the duration of the storage call,
// as in PurgeDrainer: that is what stops a second replica from issuing a
// duplicate abort while this one is in flight.
func (w *MultipartAbortDrainer) Sweep(ctx context.Context) {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		w.log().Warn("multipart abort drainer: begin", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := w.Q.WithTx(tx)
	rows, err := q.ClaimDueMultipartAborts(ctx, w.BatchSize)
	if err != nil {
		w.log().Warn("multipart abort drainer: claim", zap.Error(err))
		return
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return
		}
		tenantID := uuid.UUID(r.TenantID.Bytes)
		if err := w.Storage.AbortMultipart(ctx, r.BackendName, r.BucketName, tenantID,
			r.StorageUploadID, r.CollectionName, r.Path); err != nil {
			w.log().Warn("multipart abort drainer: abort failed; debt kept",
				zap.String("storage_upload_id", r.StorageUploadID),
				zap.String("tenant_id", tenantID.String()),
				zap.Int32("attempts", r.Attempts),
				zap.Error(err))
			msg := err.Error()
			if berr := q.BackoffMultipartAbortDebt(ctx, r.ID,
				&msg, w.backoff(r.Attempts).Microseconds()); berr != nil {
				w.log().Warn("multipart abort drainer: backoff", zap.Error(berr))
			}
			continue
		}
		if err := q.DeleteMultipartAbortDebt(ctx, r.ID); err != nil {
			// The abort landed but the row stayed. Harmless: the next tick
			// aborts an upload S3 no longer has, which it reports as success,
			// and the row goes then.
			w.log().Warn("multipart abort drainer: settle", zap.Error(err))
			continue
		}
		w.log().Info("aborted an orphaned multipart upload",
			zap.String("storage_upload_id", r.StorageUploadID),
			zap.String("tenant_id", tenantID.String()))
	}
	if err := tx.Commit(ctx); err != nil {
		w.log().Warn("multipart abort drainer: commit", zap.Error(err))
	}
}

// backoff grows with the attempt count and is capped by MaxBackoff.
func (w *MultipartAbortDrainer) backoff(attempts int32) time.Duration {
	d := time.Minute
	for range attempts {
		d *= 2
		if d >= w.MaxBackoff {
			return w.MaxBackoff
		}
	}
	return d
}

func (w *MultipartAbortDrainer) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}
