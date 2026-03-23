package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/validation"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *objectsService) CreateSingle(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, tags map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.CreateObjectResponse, error) {
	ctx, op := beginOp(ctx, OpCreateObject, "create_single",
		attribute.String("tenant_id", tenantID),
		attribute.String("category", category),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		op.fail(err)
		return domain.CreateObjectResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	if err := validation.CategorySlug(category); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid category", err)
	}

	if externalRef != nil {
		if err := validation.ExternalRef(*externalRef); err != nil {
			return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	if err := validation.Labels(labels); err != nil {
		return domain.CreateObjectResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// Idempotency-Key cache check
	if idempotencyKey != nil && *idempotencyKey != "" && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res domain.CreateObjectResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	if externalRef != nil {
		if res, handled, err := s.handleExternalRef(ctx, tenantID, *externalRef, contentType, sizeBytes, uploadTTL); handled {
			return res, err
		}
	}

	id := uuid.New()
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
			return fmt.Errorf("check category existence category=%s: %w", category, err)
		}
		catExists = exists
		return nil
	})

	g.Go(func() error {
		var err error
		signed, err = executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
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
		Status: domain.ObjectPending, Labels: labels, Tags: tags, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
		Category:  category,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return domain.CreateObjectResponse{}, fmt.Errorf("create object record objectID=%s: %w", id.String(), err)
	}

	logger.FromContext(ctx).Info(LogObjectCreated, zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	res := domain.CreateObjectResponse{
		ID: id, Key: key, Bucket: bucket, Category: category,
		Upload: domain.Presigned{URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt},
	}

	if idempotencyKey != nil && *idempotencyKey != "" && s.idemRepo != nil {
		body, err := json.Marshal(res)
		if err == nil {
			if err := s.idemRepo.Save(ctx, domain.IdempotencyRecord{
				TenantID: tenantID, Key: *idempotencyKey,
				RequestPath: "/v1/objects", ResponseBody: body, ResponseCode: 200,
				ExpiresAt: time.Now().Add(s.idempotencyTTL),
			}); err != nil {
				s.log.Warn("save idempotency record failed", zap.Error(err))
			}
		}
	}

	op.succeed()
	op.addAttrs(attribute.String("object_id", id.String()))
	return res, nil
}

func (s *objectsService) handleExternalRef(ctx context.Context, tenantID, externalRef, contentType string, sizeBytes int64, uploadTTL int) (domain.CreateObjectResponse, bool, error) {
	existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, externalRef)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.CreateObjectResponse{}, true, err
	}
	if existing == nil {
		return domain.CreateObjectResponse{}, false, nil
	}

	if existing.ContentType != contentType || existing.SizeBytes != sizeBytes {
		return domain.CreateObjectResponse{}, true, apperrors.Conflict("object with this external_ref already exists with different parameters", nil)
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}
	signed, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
		return s.s3.PresignPutObject(ctx, existing.ObjectKey, existing.ContentType, existing.SizeBytes, ttl)
	})
	if err != nil {
		return domain.CreateObjectResponse{}, true, err
	}

	return domain.CreateObjectResponse{
		ID: existing.ID, Key: existing.ObjectKey, Bucket: existing.Bucket,
		Category: existing.Category,
		Upload:   domain.Presigned{URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt},
	}, true, nil
}
