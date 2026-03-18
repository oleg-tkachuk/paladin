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

// signUpload generates a presigned URL for uploading to an existing object
func (s *objectsService) signUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (domain.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignUpload")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "sign_upload", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return domain.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return domain.Presigned{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
		return s.s3.PresignPutObject(ctx, rec.ObjectKey, rec.ContentType, rec.SizeBytes, ttl)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = domain.StatusError

		return domain.Presigned{}, err
	}

	status = domain.StatusSuccess
	span.SetStatus(codes.Ok, "")

	return domain.Presigned{
		URL:       presigned.URL,
		Method:    presigned.Method,
		Headers:   presigned.Headers,
		ExpiresAt: presigned.ExpiresAt,
	}, nil
}
