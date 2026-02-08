package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// completeMultipart completes a multipart upload
func (s *objectsService) completeMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "CompleteMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("parts_count", len(parts)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("complete_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.longOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
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

	s3Parts := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		s3Parts[i] = types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		}
	}

	if err := s.s3.CompleteMultipartUpload(ctx, multi.ObjectKey, uploadID, s3Parts); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	// Double check S3 for final ETag/Size
	head, err := s.s3.HeadObject(ctx, multi.ObjectKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, fmt.Errorf("s3 head after complete: %w", err)
	}

	if _, err := s.objRepo.MarkComplete(ctx, tenantID, multi.ObjectID, head.ETag, head.SizeBytes); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if err := s.multiRepo.MarkCompleted(ctx, tenantID, uploadID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, multi.ObjectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.String("object_id", multi.ObjectID.String()))
	return rec, nil
}
