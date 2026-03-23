package paladinapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/status"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// BulkHandler implements paladinapiconnect.BulkServiceHandler.
type BulkHandler struct {
	log *zap.Logger
	svc domain.ObjectsService
}

// NewBulkHandler creates a new BulkHandler.
func NewBulkHandler(log *zap.Logger, svc domain.ObjectsService) *BulkHandler {
	return &BulkHandler{log: log, svc: svc}
}

const (
	codeOK       = 0
	codeNotFound = 5
	codeInternal = 13
)

func (h *BulkHandler) BatchDeleteObjects(ctx context.Context, req *connect.Request[BatchDeleteObjectsRequest]) (*connect.Response[BatchDeleteObjectsResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	var successCount, failureCount int64
	results := make([]*BatchDeleteResult, 0, len(msg.Keys))

	for _, key := range msg.Keys {
		result := &BatchDeleteResult{Key: key}

		// Resolve object by key-based addressing (with resilient fallback)
		obj, err := getObjectResiliently(ctx, h.svc, tenantID, msg.Bucket, key)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: codeNotFound, Message: "object not found: " + key}
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
			result.Error = &status.Status{Code: codeInternal, Message: "internal error"}
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
			result.Error = &status.Status{Code: codeInternal, Message: "internal error"}
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
		obj, err := getObjectResiliently(ctx, h.svc, tenantID, msg.Bucket, key)
		if err != nil {
			logger.FromContext(ctx).Warn("BatchRestoreObjects: object not found",
				zap.String("key", key), zap.Error(err))
			result.Success = false
			result.Error = &status.Status{Code: codeNotFound, Message: "object not found: " + key}
			failureCount++
			results = append(results, result)

			continue
		}

		err = h.svc.Restore(ctx, tenantID, obj.ID)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: codeInternal, Message: "internal error"}
			failureCount++
		} else {
			result.Success = true
			// Re-fetch to get status: AVAILABLE
			updated, _ := getObjectResiliently(ctx, h.svc, tenantID, msg.Bucket, key)
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
