package service

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	ctx, op := beginOp(ctx, "GetMultipart", "get_multipart",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, err
	}

	rec, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return rec, nil
}
