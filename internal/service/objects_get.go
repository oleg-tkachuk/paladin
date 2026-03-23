package service

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "Get", "get",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return nil, fmt.Errorf("get object record objectID=%s: %w", id.String(), err)
	}

	if rec.Status == domain.ObjectHardDeleted {
		op.fail(domain.ErrNotFound)
		return nil, domain.ErrNotFound
	}

	op.succeed()
	return rec, nil
}

func (s *objectsService) GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "GetMeta", "get_meta",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	rec, err := s.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return rec, nil
}

func (s *objectsService) GetByKey(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "GetByKey", "get_by_key",
		attribute.String("tenant_id", tenantID),
		attribute.String("bucket", bucket),
		attribute.String("key", key),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, err
	}

	rec, err := s.objRepo.GetByKey(ctx, tenantID, bucket, key)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	if rec.Status == domain.ObjectHardDeleted {
		op.fail(domain.ErrNotFound)
		return nil, domain.ErrNotFound
	}

	op.succeed()
	return rec, nil
}
