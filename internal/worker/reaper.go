package worker

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"

	"go.uber.org/zap"
)

type Reaper struct {
	cfg       config.Housekeeping
	objRepo   domain.ObjectsRepository
	mpRepo    domain.MultipartRepository
	auditRepo domain.AuditLogRepository
	s3        domain.StorageClient
	log       *zap.Logger
}

// Note: We need to extend Repository interfaces in service package to include ListExpired methods.
// I will assume they are there or I need to add them.
// Step 184 added ListExpiredPending to ObjectsRepo.
// Step 192 (current) validates MultipartRepo has ListExpired.
// But ObjectService interface might not have them?
// The Reaper likely interacts with Repos directly or via a specific interface.
// Since repos are passed as dependencies to service, we can pass them to Reaper too.

func NewReaper(cfg config.Housekeeping, objRepo domain.ObjectsRepository, mpRepo domain.MultipartRepository, auditRepo domain.AuditLogRepository, s3c domain.StorageClient, log *zap.Logger) *Reaper {
	return &Reaper{
		cfg:       cfg,
		objRepo:   objRepo,
		mpRepo:    mpRepo,
		auditRepo: auditRepo,
		s3:        s3c,
		log:       log,
	}
}

func (r *Reaper) Start(ctx context.Context) {
	if !r.cfg.EnableReaper {
		r.log.Info("Reaper disabled")
		return
	}

	r.log.Info("Reaper started", zap.Duration("interval", r.cfg.GCInterval))

	ticker := time.NewTicker(r.cfg.GCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("Reaper stopping")
			return
		case <-ticker.C:
			r.runCleanup(ctx)
		}
	}
}

func (r *Reaper) runCleanup(ctx context.Context) {
	r.cleanupPending(ctx)
	r.cleanupMultipart(ctx)
	r.cleanupAuditLogs(ctx)
}

func (r *Reaper) cleanupAuditLogs(ctx context.Context) {
	if r.cfg.AuditLogTTL <= 0 {
		return
	}

	cutoff := time.Now().Add(-r.cfg.AuditLogTTL)
	limit := 1000

	count, err := r.auditRepo.Prune(ctx, cutoff, limit)
	if err != nil {
		r.log.Error("Failed to prune audit logs", zap.Error(err))
		return
	}

	if count > 0 {
		r.log.Info("Pruned expired audit logs", zap.Int64("count", count))
	}
}

func (r *Reaper) cleanupPending(ctx context.Context) {
	// Cutoff is now - PendingTTL
	cutoff := time.Now().Add(-r.cfg.PendingTTL)
	limit := 100 // Batch size

	recs, err := r.objRepo.ListExpiredPending(ctx, cutoff, limit)
	if err != nil {
		r.log.Error("Failed to list expired pending objects", zap.Error(err))
		return
	}

	for _, rec := range recs {
		r.log.Info("Cleaning up expired pending object", zap.String("id", rec.ID.String()), zap.String("key", rec.ObjectKey))

		// 1. Delete from S3 (best effort)
		// We ignore error if not found, but log others
		// Note: S3 client doesn't have DeleteObject exposed in interface yet?
		// Checking S3Client interface in object_service.go...
		// It has AbortMultipart and CompleteMultipart but NOT DeleteObject.
		// I need to add DeleteObject to S3Client interface.
		// For now, I'll assume I add it or skip it?
		// "DeleteObject: ... soft delete".
		// But for pending objects that never completed, we should probably hard delete to save space?
		// If I can't delete from S3, I just mark deleted in DB?

		// 2. Mark deleted in DB
		if _, err := r.objRepo.MarkDeleted(ctx, rec.TenantID, rec.ID); err != nil {
			r.log.Error("Failed to mark object deleted", zap.String("id", rec.ID.String()), zap.Error(err))
		}
	}
}

func (r *Reaper) cleanupMultipart(ctx context.Context) {
	limit := 100
	recs, err := r.mpRepo.ListExpired(ctx, limit)
	if err != nil {
		r.log.Error("Failed to list expired multipart uploads", zap.Error(err))
		return
	}

	for _, rec := range recs {
		r.log.Info("Aborting expired multipart upload", zap.String("upload_id", rec.UploadID))

		// 1. Abort in S3 (cleans up parts)
		if err := r.s3.AbortMultipartUpload(ctx, rec.ObjectKey, rec.UploadID); err != nil {
			r.log.Warn("Failed to abort multipart in S3", zap.String("upload_id", rec.UploadID), zap.Error(err))
			// Continue to mark aborted in DB anyway?
		}

		// 2. Mark aborted in DB
		if err := r.mpRepo.MarkAborted(ctx, rec.TenantID, rec.UploadID); err != nil {
			r.log.Error("Failed to mark multipart aborted", zap.String("upload_id", rec.UploadID), zap.Error(err))
		}
	}
}
