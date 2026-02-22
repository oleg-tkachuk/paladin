package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

type objectsService struct {
	objRepo   domain.ObjectsRepository
	multiRepo domain.MultipartRepository
	s3        domain.StorageClient
	policy    domain.Policy
	idemRepo  domain.IdempotencyRepository
	catRepo   domain.CategoryRepository
	partSize  int64

	// Configurable timeouts
	fastOperationTimeout    time.Duration
	defaultOperationTimeout time.Duration
	s3OperationTimeout      time.Duration
	longOperationTimeout    time.Duration
	idempotencyTTL          time.Duration
}

func NewObjectsService(
	objRepo domain.ObjectsRepository,
	multiRepo domain.MultipartRepository,
	s3Client domain.StorageClient,
	policy domain.Policy,
	idemRepo domain.IdempotencyRepository,
	catRepo domain.CategoryRepository,
	partSize int64,
	fastTimeout, defaultTimeout, s3Timeout, longTimeout time.Duration,
	idempotencyTTL time.Duration,
) domain.ObjectsService {
	return &objectsService{
		objRepo:                 objRepo,
		multiRepo:               multiRepo,
		s3:                      s3Client,
		policy:                  policy,
		idemRepo:                idemRepo,
		catRepo:                 catRepo,
		partSize:                partSize,
		fastOperationTimeout:    fastTimeout,
		defaultOperationTimeout: defaultTimeout,
		s3OperationTimeout:      s3Timeout,
		longOperationTimeout:    longTimeout,
		idempotencyTTL:          idempotencyTTL,
	}
}

func (s *objectsService) CreateSingle(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.CreateObjectResponse, error) {
	return s.createSingle(ctx, tenantID, category, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

// Delete performs a soft delete
func (s *objectsService) Delete(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Delete")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("delete", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// Soft Delete: Only mark as deleted in database
	updated, err := s.objRepo.MarkDeleted(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Soft Deleted", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}

// Purge performs a hard delete (removes from S3 and marks hard deleted)
func (s *objectsService) Purge(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Purge")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("purge", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout) // S3 delete might take longer
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if idempotencyKey != nil {
		record, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err == nil && record != nil {
			return nil // Idempotent success
		}
	}

	// Get object record to retrieve S3 key and check state
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		// If object not found, treat as success (idempotent)
		if apperrors.IsNotFound(err) {
			status = "success"
			span.SetStatus(codes.Ok, "object already absent")
			return nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	// FSM State Transition Check
	sm := domain.NewObjectFSM(obj.Status)
	err = sm.Fire(domain.EventObjectHardDelete)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "conflict"
		return fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status { // Idempotent hard-delete
		status = "success"
		span.SetStatus(codes.Ok, "already_hard_deleted")
		return nil
	}

	// Delete from S3
	if err := s.s3.DeleteObject(ctx, obj.ObjectKey); err != nil {
		logger.FromContext(ctx).Error("Failed to delete object from S3",
			zap.String("tenant_id", tenantID),
			zap.String("object_id", id.String()),
			zap.String("object_key", obj.ObjectKey),
			zap.Error(err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	// Mark as hard deleted in database
	_, err = s.objRepo.MarkHardDeleted(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if idempotencyKey != nil {
		_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
			Key:       *idempotencyKey,
			TenantID:  tenantID,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(s.idempotencyTTL),
		})
	}

	logger.FromContext(ctx).Info("Object Purged (Hard Deleted)", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}

func (s *objectsService) Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	return s.get(ctx, tenantID, id)
}

func (s *objectsService) GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	return s.getMeta(ctx, tenantID, id)
}

func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*domain.Object, error) {
	return s.completeObject(ctx, tenantID, id, etag, sizeBytes)
}

func (s *objectsService) UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	return s.updateObjectStatus(ctx, tenantID, id, status, idempotencyKey)
}

func (s *objectsService) HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	// Wrapper for backward compatibility / interface satisfaction
	return s.Purge(ctx, tenantID, id, idempotencyKey)
}

func (s *objectsService) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter, limit int, cursor string) ([]domain.Object, string, error) {
	return s.listObjects(ctx, tenantID, filter, limit, cursor)
}

func (s *objectsService) PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	return s.patchMeta(ctx, tenantID, id, labels, externalRef)
}

func (s *objectsService) SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (domain.Presigned, error) {
	return s.signUpload(ctx, tenantID, id, uploadTTL)
}

func (s *objectsService) SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (domain.Presigned, error) {
	return s.signDownload(ctx, tenantID, id, downloadTTL)
}

func (s *objectsService) InitiateMultipart(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.MultipartInitResponse, error) {
	return s.initiateMultipart(ctx, tenantID, category, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

func (s *objectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	return s.getMultipart(ctx, tenantID, uploadID)
}

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (domain.Presigned, error) {
	return s.signPart(ctx, tenantID, uploadID, partNumber)
}

func (s *objectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]domain.SignPartResponse, error) {
	return s.signPartsBatch(ctx, tenantID, uploadID, partNumbers)
}

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []domain.CompletePart) (*domain.Object, error) {
	return s.completeMultipart(ctx, tenantID, uploadID, parts)
}

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	return s.abortMultipart(ctx, tenantID, uploadID)
}
