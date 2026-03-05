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

// initiateMultipart initiates a multipart upload.
// category must be the slug of an existing tenant category.
func (s *objectsService) initiateMultipart(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.MultipartInitResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "InitiateMultipart")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("category", category),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordObjectOp(ctx, "initiate_multipart", opStatus, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		opStatus = "error"

		return domain.MultipartInitResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return domain.MultipartInitResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	if err := utils.ValidateCategorySlug(category); err != nil {
		return domain.MultipartInitResponse{}, apperrors.ValidationFailed("invalid category", err)
	}

	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return domain.MultipartInitResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	if err := utils.ValidateLabels(labels); err != nil {
		return domain.MultipartInitResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// Idempotency-Key cache check
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res domain.MultipartInitResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err == nil && existing != nil {
			return domain.MultipartInitResponse{}, apperrors.Conflict("object with this external_ref already exists", nil)
		}
	}

	// Verify the category exists for this tenant
	exists, err := s.catRepo.Exists(ctx, tenantID, category)
	if err != nil {
		return domain.MultipartInitResponse{}, fmt.Errorf("check category: %w", err)
	}
	if !exists {
		return domain.MultipartInitResponse{}, apperrors.NotFound(fmt.Sprintf("category %q not found", category), nil)
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s/%s", tenantID, category, id.String())

	init, err := executeWithBreakerRet(ctx, s.brk, "s3_initiate_multipart", func() (domain.MultipartInit, error) {
		return s.s3.CreateMultipartUpload(ctx, key, contentType)
	})
	if err != nil {
		return domain.MultipartInitResponse{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}
	expiresAt := time.Now().Add(ttl)

	objRec := domain.Object{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: s.s3.BucketName(),
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: domain.ObjectUploading, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt, Category: category,
	}
	// Start Unit of Work for transactional creation
	uow, err := s.uowf.Begin(ctx)
	if err != nil {
		return domain.MultipartInitResponse{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer uow.Rollback(ctx)

	if err := uow.Objects().Create(ctx, objRec); err != nil {
		return domain.MultipartInitResponse{}, err
	}

	multiRec := domain.Multipart{
		ID: id, TenantID: tenantID, ObjectID: id, ObjectKey: key, UploadID: init.UploadID,
		Status: domain.MultipartInitiated, ExpiresAt: expiresAt,
		Bucket: s.s3.BucketName(), ContentType: contentType, PartSize: s.partSize,
	}
	if err := uow.Multipart().Create(ctx, multiRec); err != nil {
		return domain.MultipartInitResponse{}, err
	}

	if err := uow.Commit(ctx); err != nil {
		return domain.MultipartInitResponse{}, fmt.Errorf("commit transaction: %w", err)
	}

	res := domain.MultipartInitResponse{
		ObjectID: id, ObjectKey: key, UploadID: init.UploadID,
		Bucket: s.s3.BucketName(), PartSize: s.partSize, ExpiresAt: expiresAt,
		Category: category,
	}

	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
			TenantID: tenantID, Key: *idempotencyKey,
			RequestPath: "/v1/multipart", ResponseBody: body, ResponseCode: 200,
			ExpiresAt: time.Now().Add(s.idempotencyTTL),
		})
	}

	opStatus = "success"
	span.SetStatus(codes.Ok, "")

	return res, nil
}
