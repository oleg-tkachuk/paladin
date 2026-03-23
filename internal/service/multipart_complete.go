package service

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"go.uber.org/zap"

	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []domain.CompletePart) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "CompleteMultipart", "complete_multipart",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
		attribute.Int("parts_count", len(parts)),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.longOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return nil, errors.BadRequest("invalid upload_id", err)
	}

	if multi == nil {
		err = fmt.Errorf("multipart upload not found")
		op.fail(err)
		return nil, errors.BadRequest("invalid upload_id", err)
	}

	// FSM State Transition Check
	sm := domain.NewMultipartFSM(multi.Status)
	if err = sm.Fire(domain.EventMultipartComplete); err != nil {
		op.failStatus(err, domain.StatusConflict)
		return nil, fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == multi.Status {
		op.succeedMsg("already_completed")
		rec, _ := s.objRepo.Get(ctx, tenantID, multi.ObjectID)
		return rec, nil
	}

	if err = s.executeWithBreaker("s3_complete_multipart", func() error {
		return s.s3.CompleteMultipartUpload(ctx, multi.ObjectKey, uploadID, parts)
	}); err != nil {
		op.fail(err)
		return nil, err
	}

	head, err := executeWithBreakerRet(s.brk, "s3_head_object", func() (*domain.HeadRecord, error) {
		return s.s3.HeadObject(ctx, multi.ObjectKey)
	})
	if err != nil {
		op.fail(err)
		return nil, fmt.Errorf("s3 head after complete: %w", err)
	}

	uow, err := s.uowf.Begin(ctx)
	if err != nil {
		op.fail(err)
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err := uow.Rollback(ctx); err != nil {
			s.log.Error("rollback transaction failed", zap.Error(err))
		}
	}()

	if _, err = uow.Objects().MarkComplete(ctx, tenantID, multi.ObjectID, head.ETag, head.SizeBytes); err != nil {
		op.fail(err)
		return nil, err
	}

	if err = uow.Multipart().MarkCompleted(ctx, tenantID, uploadID); err != nil {
		op.fail(err)
		return nil, err
	}

	if err := uow.Commit(ctx); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	rec, err := s.objRepo.Get(ctx, tenantID, multi.ObjectID)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	op.addAttrs(attribute.String("object_id", multi.ObjectID.String()))
	return rec, nil
}
