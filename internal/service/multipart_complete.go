package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
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
		return nil, err
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

	if err = s.executeWithBreaker(ctx, "s3_complete_multipart", func() error {
		return s.s3.CompleteMultipartUpload(ctx, multi.ObjectKey, uploadID, parts)
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	// Double check S3 for final ETag/Size
	head, err := executeWithBreakerRet(ctx, s.brk, "s3_head", func() (*domain.HeadRecord, error) {
		return s.s3.HeadObject(ctx, multi.ObjectKey)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, fmt.Errorf("s3 head after complete: %w", err)
	}

	if _, err = s.objRepo.MarkComplete(ctx, tenantID, multi.ObjectID, head.ETag, head.SizeBytes); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if err = s.multiRepo.MarkCompleted(ctx, tenantID, uploadID); err != nil {
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
