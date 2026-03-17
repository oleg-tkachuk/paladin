package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
)

// MultipartHandler implements grpcapiconnect.MultipartUploadServiceHandler.
type MultipartHandler struct {
	log *zap.Logger
	svc domain.ObjectsService
}

// NewMultipartHandler creates a new MultipartHandler.
func NewMultipartHandler(log *zap.Logger, svc domain.ObjectsService) *MultipartHandler {
	return &MultipartHandler{log: log, svc: svc}
}

func (h *MultipartHandler) InitiateMultipartUpload(ctx context.Context, req *connect.Request[InitiateMultipartUploadRequest]) (*connect.Response[InitiateMultipartUploadResponse], error) {
	msg := req.Msg

	out, err := h.svc.InitiateMultipart(ctx, msg.TenantId, msg.Bucket, msg.ContentType, msg.SizeBytes, msg.Metadata, nil, 0, &msg.IdempotencyKey)
	if err != nil {
		logger.FromContext(ctx).Warn("InitiateMultipartUpload failed", zap.Error(err))
		return nil, grpcError(err)
	}

	obj := &Object{
		ObjectId:    out.ObjectID.String(),
		Key:         out.ObjectKey,
		Bucket:      out.Bucket,
		ContentType: msg.ContentType,
		SizeBytes:   msg.SizeBytes,
		Status:      ObjectStatus_OBJECT_STATUS_UPLOADING,
	}

	return connect.NewResponse(&InitiateMultipartUploadResponse{
		Object:              obj,
		UploadId:            out.UploadID,
		RecommendedPartSize: out.PartSize,
	}), nil
}

func (h *MultipartHandler) GeneratePartUploadUrl(ctx context.Context, req *connect.Request[GeneratePartUploadUrlRequest]) (*connect.Response[GeneratePartUploadUrlResponse], error) {
	msg := req.Msg

	p, err := h.svc.SignPart(ctx, msg.TenantId, msg.UploadId, msg.PartNumber)
	if err != nil {
		logger.FromContext(ctx).Warn("GeneratePartUploadUrl failed", zap.Error(err))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&GeneratePartUploadUrlResponse{
		UploadUrl: presignedToProto(p),
	}), nil
}

func (h *MultipartHandler) CompleteMultipartUpload(ctx context.Context, req *connect.Request[CompleteMultipartUploadRequest]) (*connect.Response[CompleteMultipartUploadResponse], error) {
	msg := req.Msg

	parts := make([]domain.CompletePart, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		parts = append(parts, domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	rec, err := h.svc.CompleteMultipart(ctx, msg.TenantId, msg.UploadId, parts)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteMultipartUpload failed", zap.Error(err))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&CompleteMultipartUploadResponse{
		Object: objectToProto(rec),
	}), nil
}

func (h *MultipartHandler) AbortMultipartUpload(ctx context.Context, req *connect.Request[AbortMultipartUploadRequest]) (*connect.Response[AbortMultipartUploadResponse], error) {
	msg := req.Msg

	if err := h.svc.AbortMultipart(ctx, msg.TenantId, msg.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipartUpload failed", zap.Error(err))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&AbortMultipartUploadResponse{}), nil
}

func (h *MultipartHandler) ListParts(ctx context.Context, req *connect.Request[ListPartsRequest]) (*connect.Response[ListPartsResponse], error) {
	msg := req.Msg

	parts, err := h.svc.ListParts(ctx, msg.TenantId, msg.UploadId)
	if err != nil {
		logger.FromContext(ctx).Warn("ListParts failed", zap.Error(err))
		return nil, grpcError(err)
	}

	protoItems := make([]*PartInfo, 0, len(parts))
	for _, p := range parts {
		item := &PartInfo{
			PartNumber: int32(p.PartNumber),
		}
		if p.ETag != nil {
			item.Etag = *p.ETag
		}
		if p.SizeBytes != nil {
			item.SizeBytes = *p.SizeBytes
		}
		protoItems = append(protoItems, item)
	}

	return connect.NewResponse(&ListPartsResponse{
		Parts: protoItems,
	}), nil
}
