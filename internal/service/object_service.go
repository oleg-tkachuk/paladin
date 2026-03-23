package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/logger"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ObjectsServiceConfig holds all dependencies and configuration for the objects service.
type ObjectsServiceConfig struct {
	ObjRepo   domain.ObjectsRepository
	MultiRepo domain.MultipartRepository
	S3        domain.StorageClient
	Policy    domain.Policy
	UoWF      domain.UoWFactory
	IdemRepo  domain.IdempotencyRepository
	CatRepo   domain.CategoryRepository
	Breaker   breaker.Factory
	PartSize  int64
	Log       *zap.Logger

	FastTimeout    time.Duration
	DefaultTimeout time.Duration
	S3Timeout      time.Duration
	LongTimeout    time.Duration
	IdempotencyTTL time.Duration
}

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

	log *zap.Logger

	fastOperationTimeout    time.Duration
	defaultOperationTimeout time.Duration
	s3OperationTimeout      time.Duration
	longOperationTimeout    time.Duration
	idempotencyTTL          time.Duration
}

func NewObjectsService(cfg ObjectsServiceConfig) domain.ObjectsService {
	return &objectsService{
		objRepo:                 cfg.ObjRepo,
		multiRepo:               cfg.MultiRepo,
		s3:                      cfg.S3,
		policy:                  cfg.Policy,
		uowf:                    cfg.UoWF,
		idemRepo:                cfg.IdemRepo,
		catRepo:                 cfg.CatRepo,
		brk:                     cfg.Breaker,
		partSize:                cfg.PartSize,
		fastOperationTimeout:    cfg.FastTimeout,
		defaultOperationTimeout: cfg.DefaultTimeout,
		s3OperationTimeout:      cfg.S3Timeout,
		longOperationTimeout:    cfg.LongTimeout,
		idempotencyTTL:          cfg.IdempotencyTTL,
		log:                     cfg.Log,
	}
}

// Delete performs a soft delete.
func (s *objectsService) Delete(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, op := beginOp(ctx, OpDeleteObject, "delete",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		op.fail(err)
		return err
	}

	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return err
	}

	sm := domain.NewObjectFSM(obj.Status)
	if err := sm.Fire(domain.EventObjectSoftDelete); err != nil {
		op.failStatus(err, domain.StatusConflict)
		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status {
		op.succeed()
		return nil
	}

	updated, err := s.objRepo.MarkSoftDeleted(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object soft-deleted", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	} else {
		logger.FromContext(ctx).Warn("Soft-delete failed: object not found", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
		return apperrors.NotFound("object not found", nil)
	}

	op.succeed()
	return nil
}

// Restore brings back a soft-deleted object.
func (s *objectsService) Restore(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, op := beginOp(ctx, OpRestoreObject, "restore",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		op.fail(err)
		return err
	}

	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return err
	}

	sm := domain.NewObjectFSM(obj.Status)
	if err := sm.Fire(domain.EventObjectRestore); err != nil {
		op.failStatus(err, domain.StatusConflict)
		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status {
		op.succeed()
		return nil
	}

	updated, err := s.objRepo.Restore(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object restored", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	} else {
		logger.FromContext(ctx).Warn("Restore failed: object not found", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	op.succeed()
	return nil
}

// Purge performs a hard delete (removes from S3 and marks hard deleted).
func (s *objectsService) Purge(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error {
	ctx, op := beginOp(ctx, OpPurgeObject, "purge",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		op.fail(err)
		return err
	}

	if idempotencyKey != nil && *idempotencyKey != "" {
		record, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err == nil && record != nil {
			return nil
		}
	}

	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return err
	}

	sm := domain.NewObjectFSM(obj.Status)
	if err := sm.Fire(domain.EventObjectHardDelete); err != nil {
		op.failStatus(err, domain.StatusConflict)
		return fmt.Errorf("%s: %w", ErrInvalidTransition, err)
	}

	state, _ := sm.State(ctx)
	if state == obj.Status {
		op.succeedMsg("already_hard_deleted")
		return nil
	}

	if err = s.executeWithBreaker(S3DeleteBreaker, func() error {
		return s.s3.DeleteObject(ctx, obj.ObjectKey)
	}); err != nil {
		op.fail(err)
		return fmt.Errorf("failed to delete object from S3: %w", err)
	}

	updated, err := s.objRepo.Delete(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
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
	op.succeed()
	return nil
}

func (s *objectsService) HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	return s.Purge(ctx, tenantID, id, idempotencyKey)
}

func (s *objectsService) BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	return s.bulkStateTransition(
		ctx, tenantID, ids,
		"BulkDelete", "bulk_delete", domain.ActionDelete, domain.EventObjectSoftDelete,
		s.objRepo.BulkMarkSoftDeleted,
	)
}

func (s *objectsService) BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	return s.bulkStateTransition(
		ctx, tenantID, ids,
		"BulkRestore", "bulk_restore", domain.ActionDelete, domain.EventObjectRestore,
		s.objRepo.BulkRestore,
	)
}

func (s *objectsService) bulkStateTransition(
	ctx context.Context,
	tenantID string,
	ids []uuid.UUID,
	opName, metricName string,
	authAction domain.Action,
	fsmEvent domain.ObjectEvent,
	repoFunc func(context.Context, string, []uuid.UUID) (int64, error),
) (int64, error) {
	ctx, op := beginOp(ctx, opName, metricName,
		attribute.String("tenant_id", tenantID),
		attribute.Int("ids_count", len(ids)),
	)
	defer op.end()

	if err := s.policy.Authorize(ctx, tenantID, authAction); err != nil {
		op.fail(err)
		return 0, err
	}

	validIDs := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		obj, err := s.objRepo.Get(ctx, tenantID, id)
		if err != nil {
			continue
		}

		sm := domain.NewObjectFSM(obj.Status)
		if err := sm.Fire(fsmEvent); err != nil {
			logger.FromContext(ctx).Warn(opName+": skipping invalid transition",
				zap.String("object_id", id.String()),
				zap.String("current_status", string(obj.Status)),
				zap.Error(err))
			continue
		}

		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		op.succeed()
		return 0, nil
	}

	count, err := repoFunc(ctx, tenantID, validIDs)
	if err != nil {
		op.fail(err)
		return 0, err
	}

	op.succeed()
	return count, nil
}

func (s *objectsService) BulkPurge(ctx context.Context, tenantID string, ids []uuid.UUID, idempotencyKey *string) (int64, error) {
	ctx, op := beginOp(ctx, "BulkPurge", "bulk_purge",
		attribute.String("tenant_id", tenantID),
		attribute.Int("ids_count", len(ids)),
	)
	defer op.end()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionDelete); err != nil {
		op.fail(err)
		return 0, err
	}

	validIDs := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		obj, err := s.objRepo.Get(ctx, tenantID, id)
		if err != nil {
			continue
		}

		sm := domain.NewObjectFSM(obj.Status)
		if err := sm.Fire(domain.EventObjectHardDelete); err != nil {
			logger.FromContext(ctx).Warn("BulkPurge: skipping invalid transition",
				zap.String("object_id", id.String()),
				zap.String("current_status", string(obj.Status)),
				zap.Error(err))
			continue
		}

		if err := s.s3.DeleteObject(ctx, obj.ObjectKey); err != nil {
			logger.FromContext(ctx).Error("Failed to delete object from S3 during bulk purge",
				zap.String("object_id", id.String()), zap.Error(err))
		}

		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		op.succeed()
		return 0, nil
	}

	count, err := s.objRepo.BulkDelete(ctx, tenantID, validIDs)
	if err != nil {
		op.fail(err)
		return 0, err
	}

	op.succeed()
	return count, nil
}

func (s *objectsService) GetStats(ctx context.Context, tenantID string) (*domain.ObjectStats, error) {
	return s.objRepo.GetStats(ctx, tenantID)
}

func (s *objectsService) GetBucketStats(ctx context.Context, tenantID, bucket string) (int64, int64, error) {
	return s.objRepo.GetBucketStats(ctx, tenantID, bucket)
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
