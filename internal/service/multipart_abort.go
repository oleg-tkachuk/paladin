package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

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
	defer func() { metrics.RecordObjectOp(ctx, "abort_multipart", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
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
		// Return BadRequest for invalid/unknown upload IDs (API contract)
		return errors.BadRequest("invalid upload_id", err)
	}

	if multi == nil {
		err := fmt.Errorf("multipart upload not found")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return errors.BadRequest("invalid upload_id", err)
	}

	// FSM State Transition Check
	sm := domain.NewMultipartFSM(multi.Status)
	err = sm.Fire(domain.EventMultipartAbort)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "conflict"
		return fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == multi.Status { // Idempotent abort
		status = "success"
		span.SetStatus(codes.Ok, "already_aborted")
		return nil
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
