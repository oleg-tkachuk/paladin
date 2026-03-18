package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
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
	sec := 0
	if msg.Ttl != nil {
		sec = int(msg.Ttl.Seconds)
	}

	url, err := h.generatePresignedUrl(ctx, "GenerateUploadUrl", msg.TenantId, msg.Bucket, msg.Key, sec, h.svc.SignUpload)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&GenerateUploadUrlResponse{
		UploadUrl: url,
	}), nil
}

func (h *PresignHandler) GenerateDownloadUrl(ctx context.Context, req *connect.Request[GenerateDownloadUrlRequest]) (*connect.Response[GenerateDownloadUrlResponse], error) {
	msg := req.Msg
	sec := 0
	if msg.Ttl != nil {
		sec = int(msg.Ttl.Seconds)
	}

	url, err := h.generatePresignedUrl(ctx, "GenerateDownloadUrl", msg.TenantId, msg.Bucket, msg.Key, sec, h.svc.SignDownload)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&GenerateDownloadUrlResponse{
		DownloadUrl: url,
	}), nil
}

func (h *PresignHandler) generatePresignedUrl(
	ctx context.Context,
	opName string,
	reqTenantId string,
	bucket, key string,
	ttlDurSeconds int,
	signFunc func(context.Context, string, uuid.UUID, int) (domain.Presigned, error),
) (*PresignedUrl, error) {
	tenantID := utils.TenantIDFromContext(ctx, reqTenantId)

	rec, err := h.svc.GetByKey(ctx, tenantID, bucket, key)
	if err != nil {
		logger.FromContext(ctx).Warn(opName+": object lookup failed", zap.Error(err))

		return nil, grpcError(err)
	}

	ttl := defaultPresignTTLSeconds
	if ttlDurSeconds > 0 {
		ttl = ttlDurSeconds
	}

	presigned, err := signFunc(ctx, tenantID, rec.ID, ttl)
	if err != nil {
		logger.FromContext(ctx).Warn(opName+": failed to sign", zap.Error(err), zap.String("object_id", rec.ID.String()))

		return nil, grpcError(err)
	}

	logger.FromContext(ctx).Info(opName+": successful", zap.String("object_id", rec.ID.String()))

	return presignedToProto(presigned), nil
}
