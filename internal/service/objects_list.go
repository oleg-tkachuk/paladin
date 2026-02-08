package service

import (
	"context"
	"time"

	"paladin/internal/metrics"
	"paladin/internal/store/postgres"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// listObjects retrieves a paginated list of objects
func (s *objectsService) listObjects(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "List")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("limit", limit))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("list", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, "", err
	}

	recs, nextCursor, err := s.objRepo.List(ctx, tenantID, filter, limit, cursor)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, "", err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.Int("result_count", len(recs)))
	return recs, nextCursor, nil
}
