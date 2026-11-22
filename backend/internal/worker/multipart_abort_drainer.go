package worker

import (
	"context"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
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

// MultipartAbortDrainer defaults, for a field left zero, and the first retry's
// delay, which doubles per attempt up to MaxBackoff.
const (
	DefaultMultipartAbortBatchSize  = 100
	DefaultMultipartAbortMaxBackoff = time.Hour
	multipartAbortFirstBackoff      = time.Minute
)

// Run blocks until ctx is cancelled. Interval <= 0 disables it, which is only
// appropriate where multipart upload is unreachable — otherwise a cascade has
// no other path to closing the S3 session.
func (w *MultipartAbortDrainer) Run(ctx context.Context) error {
	if w.Interval <= 0 || w.Pool == nil {
		return nil
	}
	if w.BatchSize <= 0 {
		w.BatchSize = DefaultMultipartAbortBatchSize
	}
	if w.MaxBackoff <= 0 {
		w.MaxBackoff = DefaultMultipartAbortMaxBackoff
	}
	return RunTicker(ctx, "multipart_abort_drainer", w.Interval, func(ctx context.Context) error {
		return w.Sweep(ctx)
	})
}

// Sweep drains one batch of due debt. Exported so a test can drive a single
// tick without the ticker.
//
// The claim holds FOR UPDATE SKIP LOCKED for the duration of the storage call,
// as in PurgeDrainer: that is what stops a second replica from issuing a
// duplicate abort while this one is in flight.
//
// Debt is claimed only from the buckets the bucket gate admits as a read, as
// MultipartReaper's sessions are: a disabled backend keeps its debt, due and
// untouched, until it is enabled again.
//
// A debt that fails is logged and kept; the rest of the batch still runs, and
// the first failure is returned. A debt deferred by the gate is not a failure.
func (w *MultipartAbortDrainer) Sweep(ctx context.Context) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		w.log().Warn("multipart abort drainer: begin", zap.Error(err))
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := w.Q.WithTx(tx)
	gate := newBucketDeferrals(w.log(), bucketgate.Read)
	var first error
	_, err = claimAdmitted(ctx, gate, w.BatchSize,
		func(limit int32, deferred []pgtype.UUID) ([]sqlc.ClaimDueMultipartAbortsRow, error) {
			return q.ClaimDueMultipartAborts(ctx, deferred, limit)
		},
		abortDebtBucket,
		func(r sqlc.ClaimDueMultipartAbortsRow) error {
			keepFirst(&first, w.abortOne(ctx, q, r))
			return nil
		})
	if err != nil {
		// Cut short: nothing this tick did is committed.
		keepFirst(&first, tickCutShort(ctx, w.log(), "multipart abort drainer: claim", err))
		return first
	}
	if err := tx.Commit(ctx); err != nil {
		w.log().Warn("multipart abort drainer: commit", zap.Error(err))
		keepFirst(&first, err)
	}
	return first
}

// abortOne aborts one debt's upload and settles the debt, or records why it
// could not; it returns the failure it logged.
func (w *MultipartAbortDrainer) abortOne(ctx context.Context, q *sqlc.Queries, r sqlc.ClaimDueMultipartAbortsRow) error {
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
		return err
	}
	if err := q.DeleteMultipartAbortDebt(ctx, r.ID); err != nil {
		// The abort landed but the row stayed. Harmless: the next tick
		// aborts an upload S3 no longer has, which it reports as success,
		// and the row goes then.
		w.log().Warn("multipart abort drainer: settle", zap.Error(err))
		return err
	}
	w.log().Info("aborted an orphaned multipart upload",
		zap.String("storage_upload_id", r.StorageUploadID),
		zap.String("tenant_id", tenantID.String()))
	return nil
}

// abortDebtBucket is the bucket an abort debt's parts are on.
func abortDebtBucket(r sqlc.ClaimDueMultipartAbortsRow) bucketGateRow {
	return bucketGateRow{
		ID: r.BucketID, BackendName: r.BackendName, BucketName: r.BucketName,
		Enabled: r.Enabled, ReadOnly: r.ReadOnly, ProvisionState: r.ProvisionState,
	}
}

// backoff grows with the attempt count and is capped by MaxBackoff.
func (w *MultipartAbortDrainer) backoff(attempts int32) time.Duration {
	d := multipartAbortFirstBackoff
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
