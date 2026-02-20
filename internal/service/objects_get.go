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

// get retrieves an object by ID with tenant validation
func (s *objectsService) get(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Get")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "get", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}

// getMeta retrieves object metadata (delegates to get)
func (s *objectsService) getMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "get_meta", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	rec, err := s.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}
