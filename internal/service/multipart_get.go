package service

import (
	"context"
	"time"

	"paladin/internal/metrics"
	"paladin/internal/store/postgres"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// getMultipart retrieves multipart upload metadata
func (s *objectsService) getMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}
