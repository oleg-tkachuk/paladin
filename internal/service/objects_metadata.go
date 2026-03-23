package service

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, tags map[string]string, externalRef *string) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "PatchMeta", "patch_meta",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	rec, err := s.objRepo.Patch(ctx, tenantID, id, labels, tags, externalRef)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return rec, nil
}
