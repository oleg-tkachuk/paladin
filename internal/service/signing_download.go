package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// signDownload generates a presigned URL for downloading a completed object
func (s *objectsService) signDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (s3.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignDownload")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_download", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	if rec.Status != postgres.ObjectComplete {
		err := fmt.Errorf("object not complete")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
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
		return s3.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return presigned, nil
}
