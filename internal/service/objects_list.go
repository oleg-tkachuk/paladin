package service

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	ctx, op := beginOp(ctx, "List", "list",
		attribute.String("tenant_id", tenantID),
		attribute.Int("limit", filter.Limit),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, "", 0, err
	}

	recs, nextCursor, totalCount, err := s.objRepo.List(ctx, tenantID, filter)
	if err != nil {
		op.fail(err)
		return nil, "", 0, err
	}

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

	op.succeed()
	op.addAttrs(attribute.Int("result_count", len(filtered)))
	return filtered, nextCursor, totalCount, nil
}
