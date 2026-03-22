package grpcapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"google.golang.org/grpc"
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
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	// Extract category from tags, then metadata, then bucket (if available in future).
	category := msg.Tags["category"]
	if category == "" {
		category = msg.Metadata["category"]
	}

	if category == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("category is required in 'category' tag"))
	}

	var externalRef *string
	if ref, ok := msg.Metadata["external_ref"]; ok {
		externalRef = &ref
	} else if ref, ok := msg.Tags["external_ref"]; ok {
		externalRef = &ref
	}

	out, err := h.svc.InitiateMultipart(ctx, tenantID, category, msg.ContentType, msg.SizeBytes, msg.Metadata, msg.Tags, externalRef, 0, &msg.IdempotencyKey)
	if err != nil {
		logger.FromContext(ctx).Warn("InitiateMultipartUpload: failed",
			zap.Error(err),
			zap.String("tenant_id", tenantID),
			zap.String("category", category))

		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("InitiateMultipartUpload: successful",
		zap.String("object_id", out.ObjectID.String()),
		zap.String("upload_id", out.UploadID),
		zap.String("bucket", out.Bucket))

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
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	p, err := h.svc.SignPart(ctx, tenantID, msg.UploadId, msg.PartNumber)
	if err != nil {
		logger.FromContext(ctx).Warn("GeneratePartUploadUrl: failed", zap.Error(err), zap.String("upload_id", msg.UploadId), zap.Int32("part", msg.PartNumber))

		return nil, grpcError(err)
	}

	return connect.NewResponse(&GeneratePartUploadUrlResponse{
		UploadUrl: presignedToProto(p),
	}), nil
}

func (h *MultipartHandler) CompleteMultipartUpload(ctx context.Context, req *connect.Request[CompleteMultipartUploadRequest]) (*connect.Response[CompleteMultipartUploadResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	parts := make([]domain.CompletePart, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		parts = append(parts, domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	rec, err := h.svc.CompleteMultipart(ctx, tenantID, msg.UploadId, parts)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteMultipartUpload: failed", zap.Error(err), zap.String("upload_id", msg.UploadId))

		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("CompleteMultipartUpload: successful",
		zap.String("upload_id", msg.UploadId),
		zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&CompleteMultipartUploadResponse{
		Object: objectToProto(rec),
	}), nil
}

func (h *MultipartHandler) AbortMultipartUpload(ctx context.Context, req *connect.Request[AbortMultipartUploadRequest]) (*connect.Response[AbortMultipartUploadResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	if err := h.svc.AbortMultipart(ctx, tenantID, msg.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipartUpload: failed", zap.Error(err), zap.String("upload_id", msg.UploadId))

		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("AbortMultipartUpload: successful", zap.String("upload_id", msg.UploadId))

	return connect.NewResponse(&AbortMultipartUploadResponse{}), nil
}

func (h *MultipartHandler) ListParts(ctx context.Context, req *connect.Request[ListPartsRequest]) (*connect.Response[ListPartsResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	parts, err := h.svc.ListParts(ctx, tenantID, msg.UploadId)
	if err != nil {
		logger.FromContext(ctx).Warn("ListParts failed", zap.Error(err))

		return nil, grpcError(err)
	}

	protoItems := make([]*PartInfo, 0, len(parts))
	for _, p := range parts {
		item := &PartInfo{
			PartNumber: int32(p.PartNumber), //nolint:gosec
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

// ────────────────────────────────────────────────────────────────────────────
// gRPC Bridge
// ────────────────────────────────────────────────────────────────────────────

type multipartGRPCServer struct {
	UnimplementedMultipartUploadServiceServer
	h *MultipartHandler
}

// RegisterGRPC registers the handler as a native gRPC server.
func (h *MultipartHandler) RegisterGRPC(srv *grpc.Server) {
	RegisterMultipartUploadServiceServer(srv, &multipartGRPCServer{h: h})
}

func (s *multipartGRPCServer) InitiateMultipartUpload(ctx context.Context, req *InitiateMultipartUploadRequest) (*InitiateMultipartUploadResponse, error) {
	res, err := s.h.InitiateMultipartUpload(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *multipartGRPCServer) GeneratePartUploadUrl(ctx context.Context, req *GeneratePartUploadUrlRequest) (*GeneratePartUploadUrlResponse, error) {
	res, err := s.h.GeneratePartUploadUrl(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *multipartGRPCServer) CompleteMultipartUpload(ctx context.Context, req *CompleteMultipartUploadRequest) (*CompleteMultipartUploadResponse, error) {
	res, err := s.h.CompleteMultipartUpload(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *multipartGRPCServer) AbortMultipartUpload(ctx context.Context, req *AbortMultipartUploadRequest) (*AbortMultipartUploadResponse, error) {
	res, err := s.h.AbortMultipartUpload(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *multipartGRPCServer) ListParts(ctx context.Context, req *ListPartsRequest) (*ListPartsResponse, error) {
	res, err := s.h.ListParts(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}
