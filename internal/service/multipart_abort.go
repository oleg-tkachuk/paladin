package service

import (
	"context"
	"time"

	"paladin/internal/logger"
	"paladin/internal/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// abortMultipart aborts a multipart upload
func (s *objectsService) abortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "AbortMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("abort_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if err := s.s3.AbortMultipartUpload(ctx, multi.ObjectKey, uploadID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	err = s.multiRepo.MarkAborted(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	logger.FromContext(ctx).Info("Multipart Upload Aborted", zap.String("tenant_id", tenantID), zap.String("upload_id", uploadID))

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}
