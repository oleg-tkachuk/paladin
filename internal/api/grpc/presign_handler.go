package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// defaultPresignTTLSeconds is the TTL passed to Sign* when the client sends 0 or
// the Duration field is nil. The domain layer falls back to its own configured default.
const defaultPresignTTLSeconds = 0

// PresignHandler implements grpcapiconnect.PresignServiceHandler.
type PresignHandler struct {
	log *zap.Logger
	svc domain.ObjectsService
}

// NewPresignHandler creates a new PresignHandler.
func NewPresignHandler(log *zap.Logger, svc domain.ObjectsService) *PresignHandler {
	return &PresignHandler{log: log, svc: svc}
}

func (h *PresignHandler) GenerateUploadUrl(ctx context.Context, req *connect.Request[GenerateUploadUrlRequest]) (*connect.Response[GenerateUploadUrlResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	rec, err := h.svc.GetByKey(ctx, tenantID, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("GenerateUploadUrl: object lookup failed", zap.Error(err))
		return nil, grpcError(err)
	}

	ttl := defaultPresignTTLSeconds
	if msg.Ttl != nil {
		ttl = int(msg.Ttl.Seconds)
	}

	presigned, err := h.svc.SignUpload(ctx, tenantID, rec.ID, ttl)
	if err != nil {
		logger.FromContext(ctx).Warn("GenerateUploadUrl: failed to sign", zap.Error(err), zap.String("object_id", rec.ID.String()))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("GenerateUploadUrl: successful", zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&GenerateUploadUrlResponse{
		UploadUrl: presignedToProto(presigned),
	}), nil
}

func (h *PresignHandler) GenerateDownloadUrl(ctx context.Context, req *connect.Request[GenerateDownloadUrlRequest]) (*connect.Response[GenerateDownloadUrlResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	rec, err := h.svc.GetByKey(ctx, tenantID, msg.Bucket, msg.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("GenerateDownloadUrl: object lookup failed", zap.Error(err))
		return nil, grpcError(err)
	}

	ttl := defaultPresignTTLSeconds
	if msg.Ttl != nil {
		ttl = int(msg.Ttl.Seconds)
	}

	presigned, err := h.svc.SignDownload(ctx, tenantID, rec.ID, ttl)
	if err != nil {
		logger.FromContext(ctx).Warn("GenerateDownloadUrl: failed to sign", zap.Error(err), zap.String("object_id", rec.ID.String()))
		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info("GenerateDownloadUrl: successful", zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&GenerateDownloadUrlResponse{
		DownloadUrl: presignedToProto(presigned),
	}), nil
}
