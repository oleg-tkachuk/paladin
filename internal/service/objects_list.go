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
func (s *objectsService) listObjects(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "List")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("limit", filter.Limit))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "list", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return nil, "", 0, err
	}

	recs, nextCursor, totalCount, err := s.objRepo.List(ctx, tenantID, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return nil, "", 0, err
	}

	// Filter out hard_deleted objects
	// Note: TotalCount from DB includes hard_deleted if they match filter,
	// but normally filter excludes them.
	// If filter.Status is nil, we also exclude soft_deleted for a cleaner default view.
	filtered := make([]domain.Object, 0, len(recs))
	for _, r := range recs {
		if r.Status == domain.ObjectHardDeleted {
			continue
		}
		if filter.Status == nil && r.Status == domain.ObjectSoftDeleted {
			continue
		}
		filtered = append(filtered, r)
	}

	status = domain.StatusSuccess
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.Int("result_count", len(filtered)))

	return filtered, nextCursor, totalCount, nil
}
