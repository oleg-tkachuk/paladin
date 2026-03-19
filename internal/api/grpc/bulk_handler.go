package grpcapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/status"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// BulkHandler implements grpcapiconnect.BulkServiceHandler.
type BulkHandler struct {
	log *zap.Logger
	svc domain.ObjectsService
}

// NewBulkHandler creates a new BulkHandler.
func NewBulkHandler(log *zap.Logger, svc domain.ObjectsService) *BulkHandler {
	return &BulkHandler{log: log, svc: svc}
}

const (
	grpcCodeOK       = 0
	grpcCodeNotFound = 5
	grpcCodeInternal = 13
)

func (h *BulkHandler) getObjectResiliently(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	rec, err := h.svc.GetByKey(ctx, tenantID, bucket, key)
	if err == nil {
		return rec, nil
	}

	// Fallback to ID-based lookup if key looks like a UUID
	if errors.Is(err, domain.ErrNotFound) {
		if id, parseErr := uuid.Parse(key); parseErr == nil {
			return h.svc.Get(ctx, tenantID, id)
		}
	}

	return nil, err
}

func (h *BulkHandler) BatchDeleteObjects(ctx context.Context, req *connect.Request[BatchDeleteObjectsRequest]) (*connect.Response[BatchDeleteObjectsResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	var successCount, failureCount int64
	results := make([]*BatchDeleteResult, 0, len(msg.Keys))

	for _, key := range msg.Keys {
		result := &BatchDeleteResult{Key: key}

		// Resolve object by key-based addressing (with resilient fallback)
		obj, err := h.getObjectResiliently(ctx, tenantID, msg.Bucket, key)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeNotFound, Message: "object not found: " + key}
			failureCount++
			results = append(results, result)

			continue
		}

		// Delete or Purge based on permanent flag
		if msg.Permanent {
			err = h.svc.Purge(ctx, tenantID, obj.ID, &msg.IdempotencyKey)
		} else {
			err = h.svc.Delete(ctx, tenantID, obj.ID)
		}

		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeInternal, Message: "internal error"}
			failureCount++
		} else {
			result.Success = true
			successCount++
		}

		results = append(results, result)
	}

	logger.FromContext(ctx).Info("BatchDeleteObjects: completed",
		zap.Int64("success_count", successCount),
		zap.Int64("failure_count", failureCount),
		zap.String("bucket", msg.Bucket))

	return connect.NewResponse(&BatchDeleteObjectsResponse{
		Results:      results,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}), nil
}

func (h *BulkHandler) BatchCopyObjects(ctx context.Context, req *connect.Request[BatchCopyObjectsRequest]) (*connect.Response[BatchCopyObjectsResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	var successCount, failureCount int64
	results := make([]*BatchCopyResult, 0, len(msg.Entries))

	for _, entry := range msg.Entries {
		result := &BatchCopyResult{
			SourceKey:      entry.SourceKey,
			DestinationKey: entry.DestinationKey,
		}

		dstBucket := entry.DestinationBucket
		if dstBucket == "" {
			dstBucket = msg.Bucket
		}

		copied, err := h.svc.CopyObject(ctx, tenantID, msg.Bucket, entry.SourceKey, dstBucket, entry.DestinationKey, nil)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeInternal, Message: "internal error"}
			failureCount++
		} else {
			result.Success = true
			result.Object = objectToProto(copied)
			successCount++
		}

		results = append(results, result)
	}

	logger.FromContext(ctx).Info("BatchCopyObjects: completed",
		zap.Int64("success_count", successCount),
		zap.Int64("failure_count", failureCount),
		zap.String("bucket", msg.Bucket))

	return connect.NewResponse(&BatchCopyObjectsResponse{
		Results:      results,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}), nil
}

func (h *BulkHandler) BatchRestoreObjects(ctx context.Context, req *connect.Request[BatchRestoreObjectsRequest]) (*connect.Response[BatchRestoreObjectsResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	var successCount, failureCount int64
	results := make([]*BatchRestoreResult, 0, len(msg.Keys))

	for _, key := range msg.Keys {
		result := &BatchRestoreResult{Key: key}

		// Resolve object by key-based addressing (with resilient fallback)
		obj, err := h.getObjectResiliently(ctx, tenantID, msg.Bucket, key)
		if err != nil {
			logger.FromContext(ctx).Warn("BatchRestoreObjects: object not found",
				zap.String("key", key), zap.Error(err))
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeNotFound, Message: "object not found: " + key}
			failureCount++
			results = append(results, result)

			continue
		}

		err = h.svc.Restore(ctx, tenantID, obj.ID)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeInternal, Message: "internal error"}
			failureCount++
		} else {
			result.Success = true
			// Re-fetch to get status: AVAILABLE
			updated, _ := h.getObjectResiliently(ctx, tenantID, msg.Bucket, key)
			if updated != nil {
				result.Object = objectToProto(updated)
			}
			successCount++
		}

		results = append(results, result)
	}

	logger.FromContext(ctx).Info("BatchRestoreObjects: completed",
		zap.Int64("success_count", successCount),
		zap.Int64("failure_count", failureCount),
		zap.String("bucket", msg.Bucket))

	return connect.NewResponse(&BatchRestoreObjectsResponse{
		Results:      results,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}), nil
}
