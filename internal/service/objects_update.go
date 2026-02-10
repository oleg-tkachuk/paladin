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

// updateObjectStatus updates the status of an object (e.g. for soft deletion)
func (s *objectsService) updateObjectStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "UpdateStatus")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
		attribute.String("status", status),
	)

	// Idempotency check
	if idempotencyKey != nil {
		span.SetAttributes(attribute.String("idempotency_key", *idempotencyKey))
		// Check if we have a cached response
		cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err != nil {
			logger.FromContext(ctx).Warn("Failed to check idempotency key", zap.Error(err))
		} else if cached != nil {
			// Update is 200 OK return object, but here we return error/nil.
			// Success means 200.
			if cached.ResponseCode >= 200 && cached.ResponseCode < 300 {
				return nil
			}
		}
	}

	start := time.Now()
	var opStatus string
	var err error

	// Ensure we capture metrics and store idempotency result
	defer func() {
		metrics.RecordObjectOperation("update_status", opStatus, time.Since(start).Seconds())

		if idempotencyKey != nil {
			respCode := 200
			if err != nil {
				respCode = 500
				// Map Conflict to 409, etc. logic should be here or in handler.
				// For now, assuming handler handles error mapping, we just need to know success vs fail for idempotency (mostly).
				// But we should try to be accurate if possible.
				// Since we return generic error, we can't be perfect without custom error types.
			}
			// Store result (best effort)
			_ = s.idemRepo.Save(ctx, postgres.IdempotencyRecord{
				TenantID:     tenantID,
				Key:          *idempotencyKey,
				RequestPath:  "PATCH",
				ResponseCode: respCode,
				ExpiresAt:    time.Now().Add(s.idempotencyTTL),
			})
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	// Update requires write access (or delete access if it's soft delete? Using 'write' implies modifying the resource)
	// But soft-delete is conceptually a delete.
	// Let's require 'delete' permission for soft-delete status, and 'write' for others?
	// For simplicity, let's use ActionWrite as it modifies the object.
	// OR ActionDelete if status is soft_deleted.
	action := ActionUpdate
	if status == "soft_deleted" {
		action = ActionDelete
	}

	if err = s.policy.Authorize(ctx, tenantID, action); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		opStatus = "error"
		return err
	}

	// Get object record to retrieve details and current status
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		opStatus = "error"
		return err
	}

	// State Machine
	switch status {
	case "soft_deleted":
		// Transitions:
		// active -> soft_deleted (ok)
		// soft_deleted -> soft_deleted (ok, no-op)
		// hard_deleted -> soft_deleted (conflict)

		switch obj.Status {
		case postgres.ObjectHardDeleted, postgres.ObjectDeleted:
			err = fmt.Errorf("cannot soft-delete a hard-deleted object")
			// TODO: Use custom error type for 409
			opStatus = "conflict"
			return err
		case postgres.ObjectSoftDeleted:
			// No-op
			opStatus = "success"
			return nil
		default:
			// Active/Pending/etc -> Soft Delete
			if _, err = s.objRepo.MarkSoftDeleted(ctx, tenantID, id); err != nil {
				span.RecordError(err)
				opStatus = "error"
				return err
			}
		}

	default:
		err = fmt.Errorf("unsupported status update: %s", status)
		opStatus = "error"
		return err
	}

	logger.FromContext(ctx).Info("Object Status Updated",
		zap.String("tenant_id", tenantID),
		zap.String("object_id", id.String()),
		zap.String("status", status))

	opStatus = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}
