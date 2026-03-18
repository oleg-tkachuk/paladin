package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// patchMeta updates object metadata (labels and external_ref)
func (s *objectsService) patchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "PatchMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "patch_meta", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return nil, err
	}

	rec, err := s.objRepo.Patch(ctx, tenantID, id, labels, externalRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess
	span.SetStatus(codes.Ok, "")

	return rec, nil
}
