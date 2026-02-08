package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// initiateMultipart initiates a multipart upload
func (s *objectsService) initiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "InitiateMultipart")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("initiate_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return MultipartInitResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return MultipartInitResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	// Validate external_ref for security
	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return MultipartInitResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	// Validate labels
	if err := utils.ValidateLabels(labels); err != nil {
		return MultipartInitResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// 1. Check Idempotency-Key header
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res MultipartInitResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	// 2. Check external_ref
	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err == nil && existing != nil {
			return MultipartInitResponse{}, apperrors.Conflict("object with this external_ref already exists", nil)
		}
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s", tenantID, id.String())

	init, err := s.s3.CreateMultipartUpload(ctx, key, contentType)
	if err != nil {
		return MultipartInitResponse{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}
	expiresAt := time.Now().Add(ttl)

	objRec := postgres.ObjectRecord{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: s.s3.BucketName(),
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: postgres.ObjectUploading, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
	}
	if err := s.objRepo.Create(ctx, objRec); err != nil {
		return MultipartInitResponse{}, err
	}

	multiRec := postgres.MultipartRecord{
		ID: id, TenantID: tenantID, ObjectID: id, ObjectKey: key, UploadID: init.UploadID,
		Status: postgres.MultipartInitiated, ExpiresAt: expiresAt,
		Bucket: init.Bucket, ContentType: contentType, PartSize: s.partSize,
	}
	if err := s.multiRepo.Create(ctx, multiRec); err != nil {
		return MultipartInitResponse{}, err
	}

	res := MultipartInitResponse{
		ObjectID:  id,
		ObjectKey: key,
		UploadID:  init.UploadID,
		Bucket:    s.s3.BucketName(),
		PartSize:  s.partSize,
		ExpiresAt: expiresAt,
	}

	// 4. Save response for Idempotency-Key
	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, postgres.IdempotencyRecord{
			TenantID:     tenantID,
			Key:          *idempotencyKey,
			RequestPath:  "/v1/multipart",
			ResponseBody: body,
			ResponseCode: 200,
			ExpiresAt:    time.Now().Add(s.idempotencyTTL),
		})
	}

	return res, nil
}
