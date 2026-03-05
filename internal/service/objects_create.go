package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// createSingle creates a single object upload with a presigned PUT URL.
// category must be the slug of an existing tenant category.
func (s *objectsService) createSingle(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.CreateObjectResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, OpCreateObject)
	defer span.End()

	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("category", category),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordObjectOp(ctx, "create_single", opStatus, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		opStatus = "error"

		return domain.CreateObjectResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	// Validate category slug format server-side before any DB lookup
	if err := utils.ValidateCategorySlug(category); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid category", err)
	}

	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	if err := utils.ValidateLabels(labels); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// Idempotency-Key cache check
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res domain.CreateObjectResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	// external_ref idempotency
	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err != nil {
			return domain.CreateObjectResponse{}, err
		}
		if existing != nil {
			if existing.ContentType == contentType && existing.SizeBytes == sizeBytes {
				ttl := time.Duration(uploadTTL) * time.Second
				if ttl == 0 {
					ttl = s.s3.PresignTTLDuration()
				}
				signed, err := executeWithBreakerRet(ctx, s.brk, "s3_presign", func() (domain.Presigned, error) {
					return s.s3.PresignPutObject(ctx, existing.ObjectKey, existing.ContentType, existing.SizeBytes, ttl)
				})
				if err != nil {
					return domain.CreateObjectResponse{}, err
				}

				return domain.CreateObjectResponse{
					ID: existing.ID, Key: existing.ObjectKey, Bucket: existing.Bucket,
					Category: existing.Category,
					Upload:   domain.Presigned{URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt},
				}, nil
			}

			return domain.CreateObjectResponse{}, apperrors.Conflict("object with this external_ref already exists with different parameters", nil)
		}
	}

	id := uuid.New()
	// Key is computed by DB trigger, but we pass the intended value for consistency in presign
	key := fmt.Sprintf("%s/%s/%s", tenantID, category, id.String())

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	// Parallelize S3 presign and Category existence check
	g, gCtx := errgroup.WithContext(ctx)
	var catExists bool
	var signed domain.Presigned

	g.Go(func() error {
		exists, err := s.catRepo.Exists(gCtx, tenantID, category)
		if err != nil {
			return fmt.Errorf("check category: %w", err)
		}
		catExists = exists
		return nil
	})

	g.Go(func() error {
		var err error
		signed, err = executeWithBreakerRet(gCtx, s.brk, "s3_presign", func() (domain.Presigned, error) {
			return s.s3.PresignPutObject(gCtx, key, contentType, sizeBytes, ttl)
		})
		return err
	})

	if err := g.Wait(); err != nil {
		return domain.CreateObjectResponse{}, err
	}

	if !catExists {
		return domain.CreateObjectResponse{}, apperrors.NotFound(fmt.Sprintf("category %q not found", category), nil)
	}

	bucket := s.s3.BucketName()
	expiresAt := time.Now().Add(ttl)
	rec := domain.Object{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: bucket,
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: domain.ObjectPending, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
		Category:  category,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return domain.CreateObjectResponse{}, fmt.Errorf("create object: %w", err)
	}

	logger.FromContext(ctx).Info(LogObjectCreated, zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	res := domain.CreateObjectResponse{
		ID: id, Key: key, Bucket: bucket, Category: category,
		Upload: domain.Presigned{URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt},
	}

	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
			TenantID: tenantID, Key: *idempotencyKey,
			RequestPath: "/v1/objects", ResponseBody: body, ResponseCode: 200,
			ExpiresAt: time.Now().Add(s.idempotencyTTL),
		})
	}

	opStatus = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.String("object_id", id.String()))

	return res, nil
}
