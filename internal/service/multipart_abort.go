package service

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	ctx, op := beginOp(ctx, "AbortMultipart", "abort_multipart",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return errors.BadRequest("invalid upload_id", err)
	}

	if multi == nil {
		err = fmt.Errorf("multipart upload not found")
		op.fail(err)
		return errors.BadRequest("invalid upload_id", err)
	}

	// FSM State Transition Check
	sm := domain.NewMultipartFSM(multi.Status)
	if err = sm.Fire(domain.EventMultipartAbort); err != nil {
		op.failStatus(err, domain.StatusConflict)
		return fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == multi.Status {
		op.succeedMsg("already_aborted")
		return nil
	}

	if err = s.executeWithBreaker("s3_abort_multipart", func() error {
		return s.s3.AbortMultipartUpload(ctx, multi.ObjectKey, uploadID)
	}); err != nil {
		op.fail(err)
		return err
	}

	if err = s.multiRepo.MarkAborted(ctx, tenantID, uploadID); err != nil {
		op.fail(err)
		return err
	}

	logger.FromContext(ctx).Info("Multipart Upload Aborted", zap.String("tenant_id", tenantID), zap.String("upload_id", uploadID))
	op.succeed()
	return nil
}
