package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// listObjects retrieves a paginated list of objects
func (s *objectsService) listObjects(ctx context.Context, tenantID string, filter domain.ListObjectsFilter, limit int, cursor string) ([]domain.Object, string, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "List")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("limit", limit))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "list", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
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

	// Filter out hard_deleted objects
	filtered := make([]domain.Object, 0, len(recs))
	for _, r := range recs {
		if r.Status != domain.ObjectHardDeleted {
			filtered = append(filtered, r)
		}
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.Int("result_count", len(filtered)))
	return filtered, nextCursor, nil

}
