package service

import (
	"context"
	stderrs "errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// CompleteObject confirms that the S3 upload succeeded and promotes the
// upload intent into a completed object. The intent row is deleted and
// the object row is inserted inside a single transaction (UoW), so the
// `objects` table only ever contains objects that are known-good in S3.
//
// If no intent exists for the given ID, it falls back to the legacy
// path (object already in `objects` table with status=pending) for
// backwards compatibility during the migration period.
func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "CompleteObject", "complete_object",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	// Try intent-based flow first.
	intent, err := s.intentRepo.Get(ctx, tenantID, id)
	if err != nil && !stderrs.Is(err, domain.ErrNotFound) {
		op.fail(err)
		return nil, err
	}

	if intent != nil {
		obj, err := s.completeFromIntent(ctx, tenantID, intent, etag, sizeBytes)
		if err != nil {
			op.fail(err)
			return nil, err
		}
		op.succeed()
		return obj, nil
	}

	// Fallback: the object may already exist in the objects table (legacy
	// path or re-complete after a partial failure).
	return s.completeLegacy(ctx, op, tenantID, id, etag, sizeBytes)
}

// CompleteObjectByKey resolves an upload intent by its S3 key and promotes
// it into a completed object.
func (s *objectsService) CompleteObjectByKey(ctx context.Context, tenantID, bucket, key string, etag *string, sizeBytes *int64) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "CompleteObjectByKey", "complete_object_by_key",
		attribute.String("tenant_id", tenantID),
		attribute.String("bucket", bucket),
		attribute.String("key", key),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return nil, err
	}

	// Try intent-based flow.
	intent, err := s.intentRepo.GetByKey(ctx, tenantID, bucket, key)
	if err != nil && !stderrs.Is(err, domain.ErrNotFound) {
		op.fail(err)
		return nil, err
	}

	if intent != nil {
		obj, err := s.completeFromIntent(ctx, tenantID, intent, etag, sizeBytes)
		if err != nil {
			op.fail(err)
			return nil, err
		}
		op.succeed()
		return obj, nil
	}

	// Fallback: try existing object by key (already completed, or legacy pending).
	rec, err := s.objRepo.GetByKey(ctx, tenantID, bucket, key)
	if err != nil {
		// UUID-based fallback. Some callers mistakenly pass the object/intent
		// UUID in the `key` field instead of the full S3 object key. When that
		// happens we try the intent repo by ID first (uploads not yet
		// completed), then fall back to completeLegacy (already-pending
		// objects).
		if stderrs.Is(err, domain.ErrNotFound) {
			if id, parseErr := uuid.Parse(key); parseErr == nil {
				// Try intent-by-UUID first so freshly-initiated uploads resolve.
				if intentByID, intentErr := s.intentRepo.Get(ctx, tenantID, id); intentErr == nil && intentByID != nil {
					obj, completeErr := s.completeFromIntent(ctx, tenantID, intentByID, etag, sizeBytes)
					if completeErr != nil {
						op.fail(completeErr)
						return nil, completeErr
					}
					op.succeed()
					return obj, nil
				} else if intentErr != nil && !stderrs.Is(intentErr, domain.ErrNotFound) {
					op.fail(intentErr)
					return nil, intentErr
				}

				obj, legacyErr := s.completeLegacy(ctx, op, tenantID, id, etag, sizeBytes)
				if legacyErr != nil {
					op.fail(legacyErr)
					return nil, legacyErr
				}
				return obj, nil
			}
		}
		op.fail(err)
		if errors.IsNotFound(err) || stderrs.Is(err, domain.ErrNotFound) {
			return nil, errors.NotFound("upload intent or object not found", err)
		}
		return nil, err
	}

	// Object exists — delegate to legacy complete by ID.
	obj, err := s.completeLegacy(ctx, op, tenantID, rec.ID, etag, sizeBytes)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// completeFromIntent verifies the S3 upload, then atomically deletes the
// intent and inserts the completed object inside a single UoW transaction.
func (s *objectsService) completeFromIntent(ctx context.Context, tenantID string, intent *domain.UploadIntent, etag *string, sizeBytes *int64) (*domain.Object, error) {
	// 1. HEAD the S3 object to confirm the upload.
	head, err := s.s3.HeadObject(ctx, intent.ObjectKey)
	if err != nil {
		if errors.IsS3NotFound(err) {
			return nil, errors.PreconditionFailed("object not yet uploaded to storage", err)
		}
		return nil, fmt.Errorf("s3 head check: %w", err)
	}

	// 2. Validate caller-supplied ETag/size against actual.
	if etag != nil && utils.NormalizeETag(*etag) != utils.NormalizeETag(head.ETag) {
		return nil, fmt.Errorf("etag mismatch: expected %s, got %s", *etag, head.ETag)
	}
	if sizeBytes != nil && *sizeBytes != head.SizeBytes {
		return nil, fmt.Errorf("size mismatch: expected %d, got %d", *sizeBytes, head.SizeBytes)
	}

	// 3. Begin UoW: DELETE intent + INSERT object (status=complete).
	uow, err := s.uowf.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = uow.Rollback(ctx) }() //nolint:errcheck // rollback after commit is a no-op

	if _, err := uow.UploadIntents().Delete(ctx, tenantID, intent.ID); err != nil {
		return nil, fmt.Errorf("delete upload intent: %w", err)
	}

	now := time.Now()
	obj := domain.Object{
		ID:              intent.ID,
		TenantID:        tenantID,
		ObjectKey:       intent.ObjectKey,
		Bucket:          intent.Bucket,
		ContentType:     intent.ContentType,
		SizeBytes:       intent.SizeBytes,
		Status:          domain.ObjectComplete,
		Labels:          intent.Labels,
		Tags:            intent.Tags,
		ExternalRef:     intent.ExternalRef,
		StoredETag:      &head.ETag,
		StoredSizeBytes: &head.SizeBytes,
		CompletedAt:     &now,
		Category:        intent.Category,
		Subpath:         intent.Subpath,
	}

	if err := uow.Objects().Create(ctx, obj); err != nil {
		return nil, fmt.Errorf("create completed object: %w", err)
	}

	if err := uow.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit complete transaction: %w", err)
	}

	logger.FromContext(ctx).Info("Object Upload Completed (intent→object)",
		zap.String("tenant_id", tenantID),
		zap.String("object_id", intent.ID.String()),
	)

	// Re-read the final object to pick up trigger-computed fields.
	finalRec, err := s.objRepo.Get(ctx, tenantID, intent.ID)
	if err != nil {
		return nil, fmt.Errorf("re-read completed object: %w", err)
	}

	return finalRec, nil
}

// completeLegacy handles the pre-intent code path where the object already
// exists in the `objects` table with status=pending. This path is kept for
// backwards compatibility and will be removed once all pending objects have
// drained.
func (s *objectsService) completeLegacy(ctx context.Context, op *opCtx, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*domain.Object, error) {
	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		if errors.IsNotFound(err) || stderrs.Is(err, domain.ErrNotFound) {
			return nil, errors.NotFound("object not found", err)
		}
		return nil, err
	}

	// FSM State Transition Check
	sm := domain.NewObjectFSM(rec.Status)
	if err = sm.Fire(domain.EventObjectUploadComplete); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("invalid transition: %w", err)
	}

	state, _ := sm.State(ctx)
	if state == domain.ObjectComplete && rec.Status == domain.ObjectComplete {
		op.succeedMsg("already_complete")
		return rec, nil
	}

	head, err := s.s3.HeadObject(ctx, rec.ObjectKey)
	if err != nil {
		op.fail(err)
		if errors.IsS3NotFound(err) {
			return nil, errors.PreconditionFailed("object not yet uploaded to storage", err)
		}
		return nil, fmt.Errorf("s3 head check: %w", err)
	}

	if etag != nil && utils.NormalizeETag(*etag) != utils.NormalizeETag(head.ETag) {
		err = fmt.Errorf("etag mismatch: expected %s, got %s", *etag, head.ETag)
		op.fail(err)
		return nil, err
	}
	if sizeBytes != nil && *sizeBytes != head.SizeBytes {
		err = fmt.Errorf("size mismatch: expected %d, got %d", *sizeBytes, head.SizeBytes)
		op.fail(err)
		return nil, err
	}

	updated, err := s.objRepo.MarkComplete(ctx, tenantID, id, head.ETag, head.SizeBytes)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Upload Completed (legacy)", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	finalRec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return finalRec, nil
}
