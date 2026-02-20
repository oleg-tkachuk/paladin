package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// createSingle creates a single object upload with presigned URL
func (s *objectsService) createSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.CreateObjectResponse, error) {
	// Start tracing span
	ctx, span := otel.Tracer("object-service").Start(ctx, "CreateSingle")
	defer span.End()

	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var status string
	defer func() {
		metrics.RecordObjectOp(ctx, "create_single", status, start)
	}()

	// Enforce operation timeout
	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return domain.CreateObjectResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	// Validate external_ref for security
	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	// Validate labels
	if err := utils.ValidateLabels(labels); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// 1. Check Idempotency-Key header for cached response
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res domain.CreateObjectResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	// 2. Check external_ref for idempotency
	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err != nil {
			return domain.CreateObjectResponse{}, err
		}
		if existing != nil {
			if existing.ContentType == contentType && existing.SizeBytes == sizeBytes {
				// Idempotent retry: re-generate upload URL
				ttl := time.Duration(uploadTTL) * time.Second
				if ttl == 0 {
					ttl = s.s3.PresignTTLDuration()
				}
				signed, err := s.s3.PresignPutObject(ctx, existing.ObjectKey, existing.ContentType, existing.SizeBytes, ttl)
				if err != nil {
					return domain.CreateObjectResponse{}, err
				}
				return domain.CreateObjectResponse{
					ID:     existing.ID,
					Key:    existing.ObjectKey,
					Bucket: existing.Bucket,
					Upload: domain.Presigned{
						URL:       signed.URL,
						Method:    signed.Method,
						Headers:   signed.Headers,
						ExpiresAt: signed.ExpiresAt,
					},
				}, nil
			}
			return domain.CreateObjectResponse{}, apperrors.Conflict("object with this external_ref already exists with different parameters", nil)
		}
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s", tenantID, id.String())

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	signed, err := s.s3.PresignPutObject(ctx, key, contentType, sizeBytes, ttl)
	if err != nil {
		return domain.CreateObjectResponse{}, err
	}

	bucket := s.s3.BucketName()
	expiresAt := time.Now().Add(ttl)
	rec := domain.Object{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: bucket,
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: domain.ObjectPending, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return domain.CreateObjectResponse{}, err
	}

	res := domain.CreateObjectResponse{
		ID:     id,
		Key:    key,
		Bucket: bucket,
		Upload: domain.Presigned{
			URL:       signed.URL,
			Method:    signed.Method,
			Headers:   signed.Headers,
			ExpiresAt: signed.ExpiresAt,
		},
	}

	// 4. Save response for Idempotency-Key
	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
			TenantID:     tenantID,
			Key:          *idempotencyKey,
			RequestPath:  "/v1/objects",
			ResponseBody: body,
			ResponseCode: 200,
			ExpiresAt:    time.Now().Add(s.idempotencyTTL),
		})
	}

	// Mark operation as successful
	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.String("object_id", id.String()))

	return res, nil
}
