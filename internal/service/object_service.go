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

	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

type objectsService struct {
	objRepo   domain.ObjectsRepository
	multiRepo domain.MultipartRepository
	s3        domain.StorageClient
	policy    domain.Policy
	uowf      domain.UoWFactory
	idemRepo  domain.IdempotencyRepository
	catRepo   domain.CategoryRepository
	brk       breaker.Factory
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
	uowf domain.UoWFactory,
	idemRepo domain.IdempotencyRepository,
	catRepo domain.CategoryRepository,
	brk breaker.Factory,
	partSize int64,
	fastTimeout, defaultTimeout, s3Timeout, longTimeout time.Duration,
	idempotencyTTL time.Duration,
) domain.ObjectsService {
	return &objectsService{
		objRepo:                 objRepo,
		multiRepo:               multiRepo,
		s3:                      s3Client,
		policy:                  policy,
		uowf:                    uowf,
		idemRepo:                idemRepo,
		catRepo:                 catRepo,
		brk:                     brk,
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
	ctx, span := otel.Tracer(TracerName).Start(ctx, OpDeleteObject)
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

	// Get object to check current status
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}

	// FSM State Transition Check
	sm := domain.NewObjectFSM(obj.Status)
	err = sm.Fire(domain.EventObjectSoftDelete)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "conflict"

		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status { // Idempotent
		status = "success"

		return nil
	}

	// Soft Delete: Only mark as soft deleted in database
	updated, err := s.objRepo.MarkSoftDeleted(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object soft-deleted", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	} else {
		logger.FromContext(ctx).Warn("Soft-delete failed: object not found", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
		return apperrors.NotFound("object not found", nil)
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return nil
}

// Restore brings back a soft-deleted object
func (s *objectsService) Restore(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, span := otel.Tracer(TracerName).Start(ctx, OpRestoreObject)
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("restore", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}

	// Get object to check current status
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}

	// FSM State Transition Check
	sm := domain.NewObjectFSM(obj.Status)
	err = sm.Fire(domain.EventObjectRestore)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "conflict"

		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status { // Idempotent
		status = "success"

		return nil
	}

	// Restore in database
	updated, err := s.objRepo.Restore(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object restored", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	} else {
		logger.FromContext(ctx).Warn("Restore failed: object not found", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return nil
}

// Purge performs a hard delete (removes from S3 and marks hard deleted)
func (s *objectsService) Purge(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error {
	ctx, span := otel.Tracer(TracerName).Start(ctx, OpPurgeObject)
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

	if idempotencyKey != nil && *idempotencyKey != "" {
		record, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err == nil && record != nil {
			return nil // Idempotent success
		}
	}

	// Get object record to retrieve S3 key and check state
	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
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

		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status { // Idempotent hard-delete
		status = "success"
		span.SetStatus(codes.Ok, "already_hard_deleted")

		return nil
	}

	// Delete from S3
	err = s.executeWithBreaker(S3DeleteBreaker, func() error {
		return s.s3.DeleteObject(ctx, obj.ObjectKey)
	})
	if err != nil {
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

	// Permanently delete record from database
	updated, err := s.objRepo.Delete(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return err
	}
	if !updated {
		return apperrors.NotFound("object not found", nil)
	}

	if idempotencyKey != nil && *idempotencyKey != "" {
		_ = s.idemRepo.Save(ctx, domain.IdempotencyRecord{
			Key:       *idempotencyKey,
			TenantID:  tenantID,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(s.idempotencyTTL),
		})
	}

	logger.FromContext(ctx).Info("Object purged (hard-deleted)",
		zap.String("tenant_id", tenantID),
		zap.String("object_id", id.String()),
		zap.String("object_key", obj.ObjectKey))
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

func (s *objectsService) GetByKey(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	return s.getByKey(ctx, tenantID, bucket, key)
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

func (s *objectsService) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	return s.listObjects(ctx, tenantID, filter)
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

func (s *objectsService) CopyObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*domain.Object, error) {
	return s.copyObject(ctx, tenantID, srcBucket, srcKey, dstBucket, dstKey, metadata)
}

func (s *objectsService) MoveObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string) (*domain.Object, error) {
	return s.moveObject(ctx, tenantID, srcBucket, srcKey, dstBucket, dstKey)
}

func (s *objectsService) ListParts(ctx context.Context, tenantID string, uploadID string) ([]domain.MultipartPart, error) {
	return s.listParts(ctx, tenantID, uploadID)
}

func (s *objectsService) executeWithBreaker(name string, fn func() error) error {
	w := s.brk.Get(name)
	_, err := fault.Execute(w, func() (interface{}, error) {
		return nil, fn()
	})

	return err
}

func executeWithBreakerRet[T any](brk breaker.Factory, name string, fn func() (T, error)) (T, error) {
	w := brk.Get(name)
	res, err := fault.Execute(w, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		var zero T

		return zero, err
	}

	return res.(T), nil
}
func (s *objectsService) BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "BulkDelete")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("ids_count", len(ids)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("bulk_delete", status, time.Since(start).Seconds()) }()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		status = "error"

		return 0, err
	}

	// Filter IDs through FSM — only soft-delete items in valid states
	validIDs := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		obj, err := s.objRepo.Get(ctx, tenantID, id)
		if err != nil {
			continue
		}

		sm := domain.NewObjectFSM(obj.Status)
		if err := sm.Fire(domain.EventObjectSoftDelete); err != nil {
			logger.FromContext(ctx).Warn("BulkDelete: skipping invalid transition",
				zap.String("object_id", id.String()),
				zap.String("current_status", string(obj.Status)),
				zap.Error(err))

			continue
		}

		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		status = "success"

		return 0, nil
	}

	count, err := s.objRepo.BulkMarkSoftDeleted(ctx, tenantID, validIDs)
	if err != nil {
		status = "error"

		return 0, err
	}

	status = "success"

	return count, nil
}

func (s *objectsService) BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "BulkRestore")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("ids_count", len(ids)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("bulk_restore", status, time.Since(start).Seconds()) }()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		status = "error"

		return 0, err
	}

	// Filter IDs through FSM — only restore items in valid states
	validIDs := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		obj, err := s.objRepo.Get(ctx, tenantID, id)
		if err != nil {
			continue
		}

		sm := domain.NewObjectFSM(obj.Status)
		if err := sm.Fire(domain.EventObjectRestore); err != nil {
			logger.FromContext(ctx).Warn("BulkRestore: skipping invalid transition",
				zap.String("object_id", id.String()),
				zap.String("current_status", string(obj.Status)),
				zap.Error(err))

			continue
		}

		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		status = "success"

		return 0, nil
	}

	count, err := s.objRepo.BulkRestore(ctx, tenantID, validIDs)
	if err != nil {
		status = "error"

		return 0, err
	}

	status = "success"

	return count, nil
}

func (s *objectsService) BulkPurge(ctx context.Context, tenantID string, ids []uuid.UUID, idempotencyKey *string) (int64, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "BulkPurge")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("ids_count", len(ids)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("bulk_purge", status, time.Since(start).Seconds()) }()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		status = "error"

		return 0, err
	}

	// FSM-gate each item, then delete from S3 for valid ones
	validIDs := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		obj, err := s.objRepo.Get(ctx, tenantID, id)
		if err != nil {
			continue // Skip if not found
		}

		sm := domain.NewObjectFSM(obj.Status)
		if err := sm.Fire(domain.EventObjectHardDelete); err != nil {
			logger.FromContext(ctx).Warn("BulkPurge: skipping invalid transition",
				zap.String("object_id", id.String()),
				zap.String("current_status", string(obj.Status)),
				zap.Error(err))

			continue
		}

		// Delete from S3
		if err := s.s3.DeleteObject(ctx, obj.ObjectKey); err != nil {
			logger.FromContext(ctx).Error("Failed to delete object from S3 during bulk purge",
				zap.String("object_id", id.String()), zap.Error(err))
		}

		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		status = "success"

		return 0, nil
	}

	// Bulk hard delete from DB
	count, err := s.objRepo.BulkDelete(ctx, tenantID, validIDs)
	if err != nil {
		status = "error"

		return 0, err
	}

	status = "success"

	return count, nil
}

func (s *objectsService) GetStats(ctx context.Context, tenantID string) (*domain.ObjectStats, error) {
	return s.objRepo.GetStats(ctx, tenantID)
}

func (s *objectsService) GetBucketStats(ctx context.Context, tenantID, bucket string) (int64, int64, error) {
	return s.objRepo.GetBucketStats(ctx, tenantID, bucket)
}
