package service

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/safecast"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (domain.Presigned, error) {
	ctx, op := beginOp(ctx, "SignPart", "sign_part",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
		attribute.Int("part_number", safecast.IntFrom32(partNumber)),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	presigned, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
		return s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, partNumber, s.s3.PresignTTLDuration())
	})
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	op.succeed()
	return presigned, nil
}

func (s *objectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]domain.SignPartResponse, error) {
	ctx, op := beginOp(ctx, "SignPartsBatch", "sign_parts_batch",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
		attribute.Int("part_count", len(partNumbers)),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	ttl := s.s3.PresignTTLDuration()
	out := make([]domain.SignPartResponse, len(partNumbers))

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for i, pn := range partNumbers {
		i, pn := i, pn
		g.Go(func() error {
			signed, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
				return s.s3.PresignUploadPart(gCtx, multi.ObjectKey, uploadID, pn, ttl)
			})
			if err != nil {
				return err
			}
			out[i] = domain.SignPartResponse{
				PartNumber: pn,
				Upload:     signed,
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return out, nil
}
