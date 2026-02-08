package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// get retrieves an object by ID with authorization
func (s *objectsService) get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Get")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
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
func (s *objectsService) getMeta(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get_meta", status, time.Since(start).Seconds()) }()

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
