package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// signPart generates a presigned URL for uploading a single multipart part
func (s *objectsService) signPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_number", int(partNumber)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_part", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	presigned, err := s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, partNumber, s.s3.PresignTTLDuration())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return presigned, nil
}

// signPartsBatch generates presigned URLs for uploading multiple multipart parts
func (s *objectsService) signPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPartsBatch")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_count", len(partNumbers)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_parts_batch", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
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

	out := make([]SignPartResponse, len(partNumbers))
	for i, pn := range partNumbers {
		signed, err := s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, pn, s.s3.PresignTTLDuration())
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			status = "error"
			return nil, err
		}
		out[i] = SignPartResponse{PartNumber: pn, Upload: signed}
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return out, nil
}
