package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// MultipartReaper aborts abandoned multipart uploads. When a client
// calls InitiateMultipartUpload but never Completes or Aborts (crash,
// network loss), the S3-side multipart session stays open and accrues
// part-storage charges indefinitely — the reconciler MarkFailed's the
// PENDING object but never tells S3 to drop the parts. This worker
// closes that leak: it lists sessions past a TTL, calls AbortMultipart
// on the backend, then deletes the DB session row (which cascades the
// multipart_parts rows). The PENDING object is left to the reconciler.
type MultipartReaper struct {
	Q         *sqlc.Queries
	Storage   MultipartAborter
	TTL       time.Duration
	Interval  time.Duration
	BatchSize int32
	Logger    *zap.Logger
}

// MultipartAborter is the slice of the storage adapter the reaper needs.
type MultipartAborter interface {
	AbortMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string) error
}

func (r *MultipartReaper) Run(ctx context.Context) error {
	if r.TTL <= 0 {
		// Disabled — no TTL configured.
		return nil
	}
	if r.Interval <= 0 {
		r.Interval = 1 * time.Hour
	}
	batch := r.BatchSize
	if batch <= 0 {
		batch = 100
	}
	return RunTicker(ctx, "multipart_reaper", r.Interval, func(ctx context.Context) error {
		r.sweep(ctx, batch)
		return nil
	})
}

func (r *MultipartReaper) sweep(ctx context.Context, batch int32) {
	cutoff := pgtype.Timestamptz{Time: time.Now().UTC().Add(-r.TTL), Valid: true}
	rows, err := r.Q.ListStaleMultipartUploads(ctx, cutoff, batch)
	if err != nil {
		r.log().Warn("list stale multipart uploads failed", zap.Error(err))
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		tenantID := uuid.UUID(row.TenantID.Bytes)
		// Abort the S3 session first. If it fails we keep the DB row so
		// the next sweep retries — deleting it would orphan the S3
		// session with no record to reclaim it from.
		if err := r.Storage.AbortMultipart(ctx, row.BucketName, tenantID,
			row.StorageUploadID, row.ObjectKey, row.Key); err != nil {
			r.log().Warn("abort multipart failed; will retry next sweep",
				zap.String("upload_id", row.UploadID),
				zap.String("tenant_id", tenantID.String()),
				zap.Error(err),
			)
			continue
		}
		if err := r.Q.DeleteMultipartUpload(ctx, row.UploadID); err != nil {
			r.log().Warn("delete multipart session row failed",
				zap.String("upload_id", row.UploadID), zap.Error(err))
			continue
		}
		r.log().Info("reaped abandoned multipart upload",
			zap.String("upload_id", row.UploadID),
			zap.String("tenant_id", tenantID.String()),
		)
	}
}

func (r *MultipartReaper) log() *zap.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}
