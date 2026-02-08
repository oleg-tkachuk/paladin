package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// deleteObject removes an object from both S3 and the database
func (s *objectsService) deleteObject(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Delete")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("delete", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// Get object record to retrieve S3 key
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// Delete from S3 first (fail fast if S3 delete fails)
	if err := s.s3.DeleteObject(ctx, obj.ObjectKey); err != nil {
		logger.FromContext(ctx).Error("Failed to delete object from S3",
			zap.String("tenant_id", tenantID),
			zap.String("object_id", id.String()),
			zap.String("object_key", obj.ObjectKey),
			zap.Error(err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	// Mark as deleted in database
	updated, err := s.objRepo.MarkDeleted(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Deleted", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}
