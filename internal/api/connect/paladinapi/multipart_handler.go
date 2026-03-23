package paladinapi

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// MultipartHandler implements paladinapiconnect.MultipartUploadServiceHandler.
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
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant (local dev), or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot initiate multipart uploads for other tenants"))
	}

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

	out, err := h.svc.InitiateMultipart(ctx, requestedTenantID, category, msg.ContentType, msg.SizeBytes, msg.Metadata, msg.Tags, externalRef, 0, &msg.IdempotencyKey)
	if err != nil {
		logger.FromContext(ctx).Warn("InitiateMultipartUpload: failed",
			zap.Error(err),
			zap.String("tenant_id", requestedTenantID),
			zap.String("category", category))

		return nil, mapError(err)
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
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot generate part URLs for other tenants"))
	}

	p, err := h.svc.SignPart(ctx, requestedTenantID, msg.UploadId, msg.PartNumber)
	if err != nil {
		logger.FromContext(ctx).Warn("GeneratePartUploadUrl: failed", zap.Error(err), zap.String("upload_id", msg.UploadId), zap.Int32("part", msg.PartNumber))

		return nil, mapError(err)
	}

	return connect.NewResponse(&GeneratePartUploadUrlResponse{
		UploadUrl: presignedToProto(p),
	}), nil
}

func (h *MultipartHandler) CompleteMultipartUpload(ctx context.Context, req *connect.Request[CompleteMultipartUploadRequest]) (*connect.Response[CompleteMultipartUploadResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot complete multipart uploads for other tenants"))
	}

	parts := make([]domain.CompletePart, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		parts = append(parts, domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	rec, err := h.svc.CompleteMultipart(ctx, requestedTenantID, msg.UploadId, parts)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteMultipartUpload: failed", zap.Error(err), zap.String("upload_id", msg.UploadId))

		return nil, mapError(err)
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
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot abort multipart uploads for other tenants"))
	}

	if err := h.svc.AbortMultipart(ctx, requestedTenantID, msg.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipartUpload: failed", zap.Error(err), zap.String("upload_id", msg.UploadId))

		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("AbortMultipartUpload: successful", zap.String("upload_id", msg.UploadId))

	return connect.NewResponse(&AbortMultipartUploadResponse{}), nil
}

func (h *MultipartHandler) ListParts(ctx context.Context, req *connect.Request[ListPartsRequest]) (*connect.Response[ListPartsResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot list parts for other tenants"))
	}

	parts, err := h.svc.ListParts(ctx, requestedTenantID, msg.UploadId)
	if err != nil {
		logger.FromContext(ctx).Warn("ListParts failed", zap.Error(err))

		return nil, mapError(err)
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
