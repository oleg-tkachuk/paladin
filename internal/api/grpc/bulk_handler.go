package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/status"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
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

func (h *BulkHandler) BatchDeleteObjects(ctx context.Context, req *connect.Request[BatchDeleteObjectsRequest]) (*connect.Response[BatchDeleteObjectsResponse], error) {
	msg := req.Msg

	var successCount, failureCount int64
	results := make([]*BatchDeleteResult, 0, len(msg.Keys))

	for _, key := range msg.Keys {
		result := &BatchDeleteResult{Key: key}

		// Resolve object by key-based addressing
		obj, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, key)
		if err != nil {
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeNotFound, Message: "object not found: " + key}
			failureCount++
			results = append(results, result)

			continue
		}

		// Delete or Purge based on permanent flag
		if msg.Permanent {
			err = h.svc.Purge(ctx, msg.TenantId, obj.ID, &msg.IdempotencyKey)
		} else {
			err = h.svc.Delete(ctx, msg.TenantId, obj.ID)
		}

		if err != nil {
			logger.FromContext(ctx).Warn("BatchDeleteObjects item failed",
				zap.String("key", key), zap.Error(err))
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeInternal, Message: err.Error()}
			failureCount++
		} else {
			result.Success = true
			successCount++
		}

		results = append(results, result)
	}

	return connect.NewResponse(&BatchDeleteObjectsResponse{
		Results:      results,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}), nil
}

func (h *BulkHandler) BatchCopyObjects(ctx context.Context, req *connect.Request[BatchCopyObjectsRequest]) (*connect.Response[BatchCopyObjectsResponse], error) {
	msg := req.Msg

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

		copied, err := h.svc.CopyObject(ctx, msg.TenantId, msg.Bucket, entry.SourceKey, dstBucket, entry.DestinationKey, nil)
		if err != nil {
			logger.FromContext(ctx).Warn("BatchCopyObjects item failed",
				zap.String("source_key", entry.SourceKey),
				zap.String("destination_key", entry.DestinationKey),
				zap.Error(err))
			result.Success = false
			result.Error = &status.Status{Code: grpcCodeInternal, Message: err.Error()}
			failureCount++
		} else {
			result.Success = true
			result.Object = objectToProto(copied)
			successCount++
		}

		results = append(results, result)
	}

	return connect.NewResponse(&BatchCopyObjectsResponse{
		Results:      results,
		SuccessCount: successCount,
		FailureCount: failureCount,
	}), nil
}
