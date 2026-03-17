package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
)

// defaultDownloadTTLSeconds is the default presigned download URL expiration.
const defaultDownloadTTLSeconds = 0

// ObjectHandler implements grpcapiconnect.ObjectServiceHandler.
// Bridges the new key-based proto API to the existing UUID-based domain layer.
type ObjectHandler struct {
	log *zap.Logger
	svc domain.ObjectsService
}

// NewObjectHandler creates a new ObjectHandler.
func NewObjectHandler(log *zap.Logger, svc domain.ObjectsService) *ObjectHandler {
	return &ObjectHandler{log: log, svc: svc}
}

func (h *ObjectHandler) UploadObject(ctx context.Context, req *connect.Request[UploadObjectRequest]) (*connect.Response[UploadObjectResponse], error) {
	msg := req.Msg

	out, err := h.svc.CreateSingle(ctx, msg.TenantId, msg.Bucket, msg.ContentType, msg.SizeBytes, msg.Metadata, nil, 0, &msg.IdempotencyKey)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to create upload record", zap.Error(err), zap.String("bucket", msg.Bucket))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("upload record created",
		zap.String("object_id", out.ID.String()),
		zap.String("bucket", out.Bucket),
		zap.String("key", out.Key))

	obj := &Object{
		ObjectId:    out.ID.String(),
		Key:         out.Key,
		Bucket:      out.Bucket,
		ContentType: msg.ContentType,
		SizeBytes:   msg.SizeBytes,
		Status:      ObjectStatus_OBJECT_STATUS_PENDING,
		Metadata:    msg.Metadata,
	}

	return connect.NewResponse(&UploadObjectResponse{
		Object:    obj,
		UploadUrl: presignedToProto(out.Upload),
	}), nil
}

func (h *ObjectHandler) DownloadObject(ctx context.Context, req *connect.Request[DownloadObjectRequest]) (*connect.Response[DownloadObjectResponse], error) {
	msg := req.Msg

	rec, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get record for download", zap.Error(err), zap.String("bucket", msg.Bucket), zap.String("key", msg.Key))
		return nil, grpcError(err)
	}

	presigned, err := h.svc.SignDownload(ctx, msg.TenantId, rec.ID, defaultDownloadTTLSeconds)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to sign download URL", zap.Error(err), zap.String("object_id", rec.ID.String()))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&DownloadObjectResponse{
		Object:      objectToProto(rec),
		DownloadUrl: presignedToProto(presigned),
	}), nil
}

func (h *ObjectHandler) GetObjectMetadata(ctx context.Context, req *connect.Request[GetObjectMetadataRequest]) (*connect.Response[GetObjectMetadataResponse], error) {
	msg := req.Msg

	rec, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get metadata record", zap.Error(err), zap.String("bucket", msg.Bucket), zap.String("key", msg.Key))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&GetObjectMetadataResponse{
		Object: objectToProto(rec),
	}), nil
}

func (h *ObjectHandler) UpdateObjectMetadata(ctx context.Context, req *connect.Request[UpdateObjectMetadataRequest]) (*connect.Response[UpdateObjectMetadataResponse], error) {
	msg := req.Msg

	rec, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get record for metadata update", zap.Error(err), zap.String("bucket", msg.Bucket), zap.String("key", msg.Key))
		return nil, grpcError(err)
	}

	// PatchMeta merges labels; the proto sends metadata as the new labels set.
	updated, err := h.svc.PatchMeta(ctx, msg.TenantId, rec.ID, msg.Metadata, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to patch object metadata", zap.Error(err), zap.String("object_id", rec.ID.String()))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("object metadata updated", zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&UpdateObjectMetadataResponse{
		Object: objectToProto(updated),
	}), nil
}

func (h *ObjectHandler) DeleteObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
	msg := req.Msg

	rec, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get record for deletion", zap.Error(err), zap.String("bucket", msg.Bucket), zap.String("key", msg.Key))
		return nil, grpcError(err)
	}

	if msg.Permanent {
		err = h.svc.Purge(ctx, msg.TenantId, rec.ID, &msg.IdempotencyKey)
	} else {
		err = h.svc.Delete(ctx, msg.TenantId, rec.ID)
	}
	if err != nil {
		logger.FromContext(ctx).Warn("object deletion failed", zap.Error(err), zap.String("object_id", rec.ID.String()), zap.Bool("permanent", msg.Permanent))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("object deleted", zap.String("object_id", rec.ID.String()), zap.Bool("permanent", msg.Permanent))

	// Re-fetch to get post-delete state (soft-delete updates status)
	updated, fetchErr := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if fetchErr != nil {
		// For permanent deletes the record is gone — return the pre-delete snapshot.
		return connect.NewResponse(&DeleteObjectResponse{
			Object: objectToProto(rec),
		}), nil
	}

	return connect.NewResponse(&DeleteObjectResponse{
		Object: objectToProto(updated),
	}), nil
}

func (h *ObjectHandler) CopyObject(ctx context.Context, req *connect.Request[CopyObjectRequest]) (*connect.Response[CopyObjectResponse], error) {
	msg := req.Msg

	dstBucket := msg.DestinationBucket
	if dstBucket == "" {
		dstBucket = msg.Bucket
	}

	copy, err := h.svc.CopyObject(ctx, msg.TenantId, msg.Bucket, msg.Key, dstBucket, msg.DestinationKey, msg.Metadata)
	if err != nil {
		logger.FromContext(ctx).Warn("object copy failed",
			zap.Error(err),
			zap.String("src_bucket", msg.Bucket),
			zap.String("src_key", msg.Key),
			zap.String("dst_bucket", dstBucket),
			zap.String("dst_key", msg.DestinationKey))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("object copied successfully",
		zap.String("src_key", msg.Key),
		zap.String("dst_key", msg.DestinationKey),
		zap.String("new_object_id", copy.ID.String()))

	return connect.NewResponse(&CopyObjectResponse{
		Object: objectToProto(copy),
	}), nil
}

func (h *ObjectHandler) MoveObject(ctx context.Context, req *connect.Request[MoveObjectRequest]) (*connect.Response[MoveObjectResponse], error) {
	msg := req.Msg

	dstBucket := msg.DestinationBucket
	if dstBucket == "" {
		dstBucket = msg.Bucket
	}

	moved, err := h.svc.MoveObject(ctx, msg.TenantId, msg.Bucket, msg.Key, dstBucket, msg.DestinationKey)
	if err != nil {
		logger.FromContext(ctx).Warn("object move failed",
			zap.Error(err),
			zap.String("src_bucket", msg.Bucket),
			zap.String("src_key", msg.Key),
			zap.String("dst_bucket", dstBucket),
			zap.String("dst_key", msg.DestinationKey))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("object moved successfully",
		zap.String("src_key", msg.Key),
		zap.String("dst_key", msg.DestinationKey),
		zap.String("new_object_id", moved.ID.String()))

	return connect.NewResponse(&MoveObjectResponse{
		Object: objectToProto(moved),
	}), nil
}

func (h *ObjectHandler) ListObjects(ctx context.Context, req *connect.Request[ListObjectsRequest]) (*connect.Response[ListObjectsResponse], error) {
	msg := req.Msg

	filter := domain.ListObjectsFilter{
		Limit:  int(msg.PageSize),
		Cursor: msg.PageToken,
	}
	if filter.Limit <= 0 {
		const defaultPageSize = 20
		filter.Limit = defaultPageSize
	}

	if f := msg.Filter; f != nil {
		if f.Prefix != "" {
			filter.KeyPrefix = &f.Prefix
		}
		if len(f.Tags) > 0 {
			filter.Tags = f.Tags
		}
		if f.MinSizeBytes != nil {
			v := *f.MinSizeBytes
			filter.MinSizeBytes = &v
		}
		if f.MaxSizeBytes != nil {
			v := *f.MaxSizeBytes
			filter.MaxSizeBytes = &v
		}
		if f.ModifiedAfter != nil {
			t := f.ModifiedAfter.AsTime()
			filter.CreatedAfter = &t
		}
		if f.ModifiedBefore != nil {
			t := f.ModifiedBefore.AsTime()
			filter.CreatedBefore = &t
		}
		if f.Status != ObjectStatus_OBJECT_STATUS_UNSPECIFIED {
			s := protoStatusToDomain(f.Status)
			filter.Status = &s
		}
		if f.KeyPattern != "" {
			filter.KeyPattern = &f.KeyPattern
		}
		if f.ContentType != "" {
			filter.ContentType = &f.ContentType
		}
	}

	if msg.OrderBy != "" {
		filter.SortBy = msg.OrderBy
	}
	if msg.SortOrder == SortOrder_SORT_ORDER_DESC {
		filter.SortOrder = "desc"
	} else if msg.SortOrder == SortOrder_SORT_ORDER_ASC {
		filter.SortOrder = "asc"
	}

	objects, nextCursor, total, err := h.svc.List(ctx, msg.TenantId, filter)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to list objects", zap.Error(err), zap.String("bucket", msg.Bucket))
		return nil, grpcError(err)
	}

	items := make([]*Object, 0, len(objects))
	for i := range objects {
		items = append(items, objectToProto(&objects[i]))
	}

	return connect.NewResponse(&ListObjectsResponse{
		Objects:       items,
		NextPageToken: nextCursor,
		TotalCount:    total,
	}), nil
}

func (h *ObjectHandler) CompleteObject(ctx context.Context, req *connect.Request[CompleteObjectRequest]) (*connect.Response[CompleteObjectResponse], error) {
	msg := req.Msg

	rec, err := h.svc.GetByKey(ctx, msg.TenantId, msg.Bucket, msg.Key)
	if err != nil {
		return nil, grpcError(err)
	}

	var etag *string
	if msg.Etag != "" {
		etag = &msg.Etag
	}

	updated, err := h.svc.CompleteObject(ctx, msg.TenantId, rec.ID, etag, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to complete object upload", zap.Error(err), zap.String("object_id", rec.ID.String()))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("object upload completed", zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&CompleteObjectResponse{
		Object: objectToProto(updated),
	}), nil
}
