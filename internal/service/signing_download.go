package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// signDownload generates a presigned URL for downloading a completed object
func (s *objectsService) signDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (domain.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignDownload")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_download", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return domain.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return domain.Presigned{}, err
	}

	if rec.Status != domain.ObjectComplete {
		err := errors.Conflict("object not complete", nil)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return domain.Presigned{}, err
	}

	ttl := time.Duration(downloadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := s.s3.PresignGetObject(ctx, rec.ObjectKey, ttl)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return domain.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return domain.Presigned{
		URL:       presigned.URL,
		Method:    presigned.Method,
		Headers:   presigned.Headers,
		ExpiresAt: presigned.ExpiresAt,
	}, nil
}
