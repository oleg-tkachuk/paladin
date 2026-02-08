package service

import (
	"context"
	"time"

	"paladin/internal/metrics"
	"paladin/internal/store/postgres"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// patchMeta updates object metadata (labels and external_ref)
func (s *objectsService) patchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "PatchMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("patch_meta", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Patch(ctx, tenantID, id, labels, externalRef)
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
