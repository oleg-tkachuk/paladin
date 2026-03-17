package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// completeMultipart completes a multipart upload
func (s *objectsService) completeMultipart(ctx context.Context, tenantID string, uploadID string, parts []domain.CompletePart) (*domain.Object, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "CompleteMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("parts_count", len(parts)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "complete_multipart", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.longOperationTimeout)
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

		return nil, errors.BadRequest("invalid upload_id", err)
	}

	if multi == nil {
		err = fmt.Errorf("multipart upload not found")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, errors.BadRequest("invalid upload_id", err)
	}

	// FSM State Transition Check
	sm := domain.NewMultipartFSM(multi.Status)
	err = sm.Fire(domain.EventMultipartComplete)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "conflict"

		return nil, fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == multi.Status { // Idempotent completion
		status = "success"
		span.SetStatus(codes.Ok, "already_completed")
		rec, _ := s.objRepo.Get(ctx, tenantID, multi.ObjectID)

		return rec, nil
	}

	err = s.executeWithBreaker("s3_complete_multipart", func() error {
		return s.s3.CompleteMultipartUpload(ctx, multi.ObjectKey, uploadID, parts)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	// Double check S3 for final ETag/Size
	head, err := executeWithBreakerRet(s.brk, "s3_head_object", func() (*domain.HeadRecord, error) {
		return s.s3.HeadObject(ctx, multi.ObjectKey)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("s3 head after complete: %w", err)
	}

	// Ensure db transaction using Unit of Work
	uow, err := s.uowf.Begin(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer uow.Rollback(ctx)

	if _, err = uow.Objects().MarkComplete(ctx, tenantID, multi.ObjectID, head.ETag, head.SizeBytes); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	if err = uow.Multipart().MarkCompleted(ctx, tenantID, uploadID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	if err := uow.Commit(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("commit transaction: %w", err)
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
