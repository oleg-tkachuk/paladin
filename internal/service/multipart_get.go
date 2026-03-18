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

// getMultipart retrieves multipart upload metadata
func (s *objectsService) getMultipart(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "get_multipart", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return nil, err
	}

	rec, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
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
