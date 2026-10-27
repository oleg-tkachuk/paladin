package worker

import (
	"context"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// MultipartReaper aborts abandoned multipart uploads. When a client
// calls InitiateMultipartUpload but never Completes or Aborts (crash,
// network loss), the S3-side multipart session stays open and accrues
// part-storage charges indefinitely — the reconciler discards the PENDING
// object but never tells S3 to drop the parts. This worker
// closes that leak: it lists sessions past a TTL, calls AbortMultipart
// on the backend, then deletes the DB session row. The PENDING object is
// left to the reconciler.
type MultipartReaper struct {
	Q *sqlc.Queries
	// Sessions owns row removal. Kept separate from Q because deleting a
	// session is not a plain DELETE — see SessionDeleter.
	Sessions  SessionDeleter
	Storage   MultipartAborter
	TTL       time.Duration
	Interval  time.Duration
	BatchSize int32
	Logger    *zap.Logger
}

// SessionDeleter removes a session row whose S3-side upload has already been
// aborted, signalling that fact to the abort-debt trigger. Satisfied by
// adapters.MultipartRepo.
type SessionDeleter interface {
	DeleteSession(ctx context.Context, uploadID string) error
}

// MultipartAborter is the slice of the storage adapter the reaper needs.
type MultipartAborter interface {
	AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error
}

// MultipartReaper defaults, for a field left zero.
const (
	DefaultMultipartReaperInterval  = time.Hour
	DefaultMultipartReaperBatchSize = 100
)

func (r *MultipartReaper) Run(ctx context.Context) error {
	if r.TTL <= 0 {
		// Disabled — no TTL configured.
		return nil
	}
	if r.Interval <= 0 {
		r.Interval = DefaultMultipartReaperInterval
	}
	if r.BatchSize <= 0 {
		r.BatchSize = DefaultMultipartReaperBatchSize
	}
	return RunTicker(ctx, "multipart_reaper", r.Interval, func(ctx context.Context) error {
		return r.Sweep(ctx)
	})
}

// Sweep reaps one batch of sessions older than TTL. Exported so a test can
// drive a single tick without the ticker.
//
// A session that fails is logged and left for the next sweep; the rest of the
// batch still runs, and the first failure is returned.
func (r *MultipartReaper) Sweep(ctx context.Context) error {
	cutoff := pgtype.Timestamptz{Time: time.Now().UTC().Add(-r.TTL), Valid: true}
	rows, err := r.Q.ListStaleMultipartUploads(ctx, cutoff, r.BatchSize)
	if err != nil {
		r.log().Warn("list stale multipart uploads failed", zap.Error(err))
		return err
	}
	var first error
	for _, row := range rows {
		if ctx.Err() != nil {
			return first
		}
		tenantID := uuid.UUID(row.TenantID.Bytes)
		// Abort the S3 session first. If it fails we keep the DB row so
		// the next sweep retries — deleting it would orphan the S3
		// session with no record to reclaim it from.
		if err := r.Storage.AbortMultipart(ctx, row.BackendName, row.BucketName, tenantID,
			row.StorageUploadID, row.CollectionName, row.Path); err != nil {
			r.log().Warn("abort multipart failed; will retry next sweep",
				zap.String("upload_id", uuid.UUID(row.ID.Bytes).String()),
				zap.String("tenant_id", tenantID.String()),
				zap.Error(err),
			)
			keepFirst(&first, err)
			continue
		}
		// Through the repo, not the queries: the delete has to run in a
		// transaction that sets `paladin.multipart_aborted`, or the abort-debt
		// trigger files a debt against the upload we have just aborted.
		if err := r.Sessions.DeleteSession(ctx, uuid.UUID(row.ID.Bytes).String()); err != nil {
			r.log().Warn("delete multipart session row failed",
				zap.String("upload_id", uuid.UUID(row.ID.Bytes).String()), zap.Error(err))
			keepFirst(&first, err)
			continue
		}
		r.log().Info("reaped abandoned multipart upload",
			zap.String("upload_id", uuid.UUID(row.ID.Bytes).String()),
			zap.String("tenant_id", tenantID.String()),
		)
	}
	return first
}

func (r *MultipartReaper) log() *zap.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}
