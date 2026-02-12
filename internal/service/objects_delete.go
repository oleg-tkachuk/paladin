package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// hardDeleteObject permantly removes an object from both S3 and the database
func (s *objectsService) hardDeleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "HardDelete")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)

	// Idempotency check
	if idempotencyKey != nil {
		span.SetAttributes(attribute.String("idempotency_key", *idempotencyKey))
		// Check if we have a cached response
		cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err != nil {
			logger.FromContext(ctx).Warn("Failed to check idempotency key", zap.Error(err))
		} else if cached != nil {
			// Basic check: if meaningful work was done, we rely on the repo to tell us.
			// But here, for Delete, a success is 204.
			// If cached.ResponseCode is 2xx, return nil.
			if cached.ResponseCode >= 200 && cached.ResponseCode < 300 {
				return nil
			}
			// If it was an error, we might want to retry, so we fall through.
		}
	}

	start := time.Now()
	var status string
	var err error

	// Ensure we capture metrics and store idempotency result
	defer func() {
		metrics.RecordObjectOperation("hard_delete", status, time.Since(start).Seconds())

		if idempotencyKey != nil {
			respCode := 204
			if err != nil {
				// Map errors to rough codes for idempotency storage
				// This is a simplification; ideally we'd need the actual handler's mapping.
				// But we can usually assume typical mappings.
				respCode = 500
			}
			// Store result (best effort)
			_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
				TenantID:     tenantID,
				Key:          *idempotencyKey,
				RequestPath:  "DELETE",
				ResponseCode: respCode,
				ExpiresAt:    time.Now().Add(s.idempotencyTTL),
			})
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err = s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// Get object record to retrieve details
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		// If not found, it's already "deleted" in a sense, so 204.
		// But strictly, 404 is standard.
		// We propagate error.
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// Transitions:
	// active -> hard_deleted (S3 delete + DB update)
	// soft_deleted -> hard_deleted (S3 delete + DB update)
	// hard_deleted -> hard_deleted (no-op)

	if obj.Status == domain.ObjectHardDeleted || obj.Status == domain.ObjectDeleted {
		status = "success"
		return nil
	}

	// Delete from S3 first (fail fast)
	if err = s.s3.DeleteObject(ctx, obj.ObjectKey); err != nil {
		logger.FromContext(ctx).Error("Failed to delete object from S3",
			zap.String("tenant_id", tenantID),
			zap.String("object_id", id.String()),
			zap.String("object_key", obj.ObjectKey),
			zap.Error(err))
		span.RecordError(err)
		status = "error"
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	// Mark as hard deleted in database
	if _, err = s.objRepo.MarkHardDeleted(ctx, tenantID, id); err != nil {
		span.RecordError(err)
		status = "error"
		return err
	}

	logger.FromContext(ctx).Info("Object Hard Deleted",
		zap.String("tenant_id", tenantID),
		zap.String("object_id", id.String()))

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}
