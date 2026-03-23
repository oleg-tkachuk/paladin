package service

import (
	"context"
	stderrs "errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "CompleteObject", "complete_object",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		if errors.IsNotFound(err) || stderrs.Is(err, domain.ErrNotFound) {
			return nil, errors.NotFound("object not found", err)
		}
		return nil, err
	}

	// FSM State Transition Check
	sm := domain.NewObjectFSM(rec.Status)
	if err = sm.Fire(domain.EventObjectUploadComplete); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == domain.ObjectComplete && rec.Status == domain.ObjectComplete {
		op.succeedMsg("already_complete")
		return rec, nil
	}

	head, err := s.s3.HeadObject(ctx, rec.ObjectKey)
	if err != nil {
		op.fail(err)
		if errors.IsS3NotFound(err) {
			return nil, errors.PreconditionFailed("object not yet uploaded to storage", err)
		}
		return nil, fmt.Errorf("s3 head check: %w", err)
	}

	if etag != nil && utils.NormalizeETag(*etag) != utils.NormalizeETag(head.ETag) {
		err = fmt.Errorf("etag mismatch: expected %s, got %s", *etag, head.ETag)
		op.fail(err)
		return nil, err
	}
	if sizeBytes != nil && *sizeBytes != head.SizeBytes {
		err = fmt.Errorf("size mismatch: expected %d, got %d", *sizeBytes, head.SizeBytes)
		op.fail(err)
		return nil, err
	}

	updated, err := s.objRepo.MarkComplete(ctx, tenantID, id, head.ETag, head.SizeBytes)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Upload Completed", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	finalRec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return finalRec, nil
}
