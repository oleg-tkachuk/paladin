package worker

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Reaper struct {
	cfg       config.Housekeeping
	objRepo   domain.ObjectsRepository
	mpRepo    domain.MultipartRepository
	auditRepo domain.AuditLogRepository
	s3        domain.StorageClient
	log       *zap.Logger
}

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

	// Run once on start
	r.runCleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("Reaper stopping", zap.Error(ctx.Err()))

			return
		case <-ticker.C:
			r.runCleanup(ctx)
		}
	}
}

func (r *Reaper) runCleanup(ctx context.Context) {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		r.cleanupPending(ctx)

		return nil
	})
	g.Go(func() error {
		r.cleanupMultipart(ctx)

		return nil
	})
	g.Go(func() error {
		r.cleanupAuditLogs(ctx)

		return nil
	})

	if err := g.Wait(); err != nil {
		r.log.Error("Reaper cleanup tasks failed", zap.Error(err))
	}
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
	cutoff := time.Now().Add(-r.cfg.PendingTTL)
	limit := 100 // Batch size

	recs, err := r.objRepo.ListExpiredPending(ctx, cutoff, limit)
	if err != nil {
		r.log.Error("Failed to list expired pending objects", zap.Error(err))

		return
	}

	if len(recs) == 0 {
		return
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Bound concurrency to avoid flooding S3/DB

	for _, rec := range recs {
		obj := rec
		g.Go(func() error {
			r.log.Info("Cleaning up expired pending object", zap.String("id", obj.ID.String()), zap.String("key", obj.ObjectKey))

			// Physically delete from DB (S3 deletion logic omitted as noted in file)
			if _, err := r.objRepo.Delete(ctx, obj.TenantID, obj.ID); err != nil {
				r.log.Error("Failed to physically delete object", zap.String("id", obj.ID.String()), zap.Error(err))

				return err
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		r.log.Warn("Some pending objects failed to clean up", zap.Error(err))
	}
}

func (r *Reaper) cleanupMultipart(ctx context.Context) {
	limit := 100
	recs, err := r.mpRepo.ListExpired(ctx, limit)
	if err != nil {
		r.log.Error("Failed to list expired multipart uploads", zap.Error(err))

		return
	}

	if len(recs) == 0 {
		return
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Bound concurrency

	for _, rec := range recs {
		m := rec
		g.Go(func() error {
			r.log.Info("Aborting expired multipart upload", zap.String("upload_id", m.UploadID))

			// 1. Abort in S3 (cleans up parts)
			if err := r.s3.AbortMultipartUpload(ctx, m.ObjectKey, m.UploadID); err != nil {
				r.log.Warn("Failed to abort multipart in S3", zap.String("upload_id", m.UploadID), zap.Error(err))
				// Continue to mark aborted in DB anyway
			}

			// 2. Mark aborted in DB
			if err := r.mpRepo.MarkAborted(ctx, m.TenantID, m.UploadID); err != nil {
				r.log.Error("Failed to mark multipart aborted", zap.String("upload_id", m.UploadID), zap.Error(err))

				return err
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		r.log.Warn("Some multipart uploads failed to clean up", zap.Error(err))
	}
}
