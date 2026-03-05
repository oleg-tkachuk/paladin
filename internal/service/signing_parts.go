package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

// signPart generates a presigned URL for uploading a single multipart part
func (s *objectsService) signPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (domain.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_number", int(partNumber)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "sign_part", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return domain.Presigned{}, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return domain.Presigned{}, err
	}

	presigned, err := executeWithBreakerRet(ctx, s.brk, "s3_presign_part", func() (domain.Presigned, error) {
		return s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, partNumber, s.s3.PresignTTLDuration())
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return domain.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return domain.Presigned{
		URL:       presigned.URL,
		Method:    presigned.Method,
		Headers:   presigned.Headers,
		ExpiresAt: presigned.ExpiresAt,
	}, nil
}

// signPartsBatch generates presigned URLs for uploading multiple multipart parts concurrently
func (s *objectsService) signPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]domain.SignPartResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPartsBatch")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_count", len(partNumbers)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "sign_parts_batch", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	ttl := s.s3.PresignTTLDuration()
	out := make([]domain.SignPartResponse, len(partNumbers))

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10) // bound concurrency to avoid overwhelming the S3 signer

	for i, pn := range partNumbers {
		g.Go(func() error {
			signed, err := executeWithBreakerRet(gCtx, s.brk, "s3_presign_part", func() (domain.Presigned, error) {
				return s.s3.PresignUploadPart(gCtx, multi.ObjectKey, uploadID, pn, ttl)
			})
			if err != nil {
				return err
			}
			out[i] = domain.SignPartResponse{
				PartNumber: pn,
				Upload: domain.Presigned{
					URL:       signed.URL,
					Method:    signed.Method,
					Headers:   signed.Headers,
					ExpiresAt: signed.ExpiresAt,
				},
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return out, nil
}
