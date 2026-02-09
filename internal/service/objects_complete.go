package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// completeObject marks an object as complete after upload verification
func (s *objectsService) completeObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "CompleteObject")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("complete_object", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if rec.Status == postgres.ObjectComplete {
		status = "success"
		span.SetStatus(codes.Ok, "already_complete")
		return rec, nil
	}

	// Double check S3
	head, err := s.s3.HeadObject(ctx, rec.ObjectKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, fmt.Errorf("s3 head check: %w", err)
	}

	// Validate ETag if provided
	if etag != nil && *etag != head.ETag {
		err := fmt.Errorf("etag mismatch: expected %s, got %s", *etag, head.ETag)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}
	// Validate Size if provided
	if sizeBytes != nil && *sizeBytes != head.SizeBytes {
		err := fmt.Errorf("size mismatch: expected %d, got %d", *sizeBytes, head.SizeBytes)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	updated, err := s.objRepo.MarkComplete(ctx, tenantID, id, head.ETag, head.SizeBytes)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Upload Completed", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	finalRec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return finalRec, nil
}
