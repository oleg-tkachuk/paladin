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

// defaultDownloadTTLSeconds is the default presigned download URL expiration.
const defaultDownloadTTLSeconds = 0

// ObjectHandler implements paladinapiconnect.ObjectServiceHandler.
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot upload objects for other tenants"))
	}

	// Extract category from tags, then metadata, then bucket.
	category := msg.Tags["category"]
	if category == "" {
		category = msg.Metadata["category"]
	}
	if category == "" {
		// Fallback to bucket field if no explicit category tag is provided
		category = msg.Bucket
	}

	if category == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("category is required (either in bucket field or as 'category' tag)"))
	}

	var externalRef *string
	if ref, ok := msg.Metadata["external_ref"]; ok {
		externalRef = &ref
	} else if ref, ok := msg.Tags["external_ref"]; ok {
		externalRef = &ref
	}

	out, err := h.svc.CreateSingle(ctx, requestedTenantID, category, msg.ContentType, msg.SizeBytes, msg.Metadata, msg.Tags, externalRef, 0, &msg.IdempotencyKey)
	if err != nil {
		return nil, mapError(err)
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot download objects of other tenants"))
	}

	rec, err := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if err != nil {
		return nil, mapError(err)
	}

	presigned, err := h.svc.SignDownload(ctx, requestedTenantID, rec.ID, defaultDownloadTTLSeconds)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&DownloadObjectResponse{
		Object:      objectToProto(rec),
		DownloadUrl: presignedToProto(presigned),
	}), nil
}

func (h *ObjectHandler) GetObjectMetadata(ctx context.Context, req *connect.Request[GetObjectMetadataRequest]) (*connect.Response[GetObjectMetadataResponse], error) {
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot access metadata of other tenants"))
	}

	rec, err := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&GetObjectMetadataResponse{
		Object: objectToProto(rec),
	}), nil
}

func (h *ObjectHandler) UpdateObjectMetadata(ctx context.Context, req *connect.Request[UpdateObjectMetadataRequest]) (*connect.Response[UpdateObjectMetadataResponse], error) {
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot update metadata of other tenants"))
	}

	rec, err := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if err != nil {
		return nil, mapError(err)
	}

	// PatchMeta merges labels/tags; the proto sends them as the new sets.
	updated, err := h.svc.PatchMeta(ctx, requestedTenantID, rec.ID, msg.Metadata, msg.Tags, nil)
	if err != nil {
		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("object metadata updated", zap.String("object_id", rec.ID.String()))

	return connect.NewResponse(&UpdateObjectMetadataResponse{
		Object: objectToProto(updated),
	}), nil
}

func (h *ObjectHandler) DeleteObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot delete objects of other tenants"))
	}

	rec, err := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if err != nil {
		return nil, mapError(err)
	}

	if msg.Permanent {
		err = h.svc.Purge(ctx, requestedTenantID, rec.ID, &msg.IdempotencyKey)
	} else {
		err = h.svc.Delete(ctx, requestedTenantID, rec.ID)
	}
	if err != nil {
		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("object deleted", zap.String("object_id", rec.ID.String()), zap.Bool("permanent", msg.Permanent))

	// Re-fetch to get post-delete state (soft-delete updates status)
	updated, fetchErr := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if fetchErr != nil {
		// For permanent deletes the record is gone — return the pre-delete snapshot.
		//nolint:nilerr
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot copy objects for other tenants"))
	}

	dstBucket := msg.DestinationBucket
	if dstBucket == "" {
		dstBucket = msg.Bucket
	}

	copied, err := h.svc.CopyObject(ctx, requestedTenantID, msg.Bucket, msg.Key, dstBucket, msg.DestinationKey, msg.Metadata)
	if err != nil {
		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("object copied successfully",
		zap.String("src_key", msg.Key),
		zap.String("dst_key", msg.DestinationKey),
		zap.String("new_object_id", copied.ID.String()))

	return connect.NewResponse(&CopyObjectResponse{
		Object: objectToProto(copied),
	}), nil
}

func (h *ObjectHandler) MoveObject(ctx context.Context, req *connect.Request[MoveObjectRequest]) (*connect.Response[MoveObjectResponse], error) {
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot move objects for other tenants"))
	}

	dstBucket := msg.DestinationBucket
	if dstBucket == "" {
		dstBucket = msg.Bucket
	}

	moved, err := h.svc.MoveObject(ctx, requestedTenantID, msg.Bucket, msg.Key, dstBucket, msg.DestinationKey)
	if err != nil {
		return nil, mapError(err)
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot list objects of other tenants"))
	}

	filter := domain.ListObjectsFilter{
		Limit:  int(msg.PageSize),
		Cursor: msg.PageToken,
	}
	if filter.Limit <= 0 {
		const defaultPageSize = 20
		filter.Limit = defaultPageSize
	}

	//nolint:nestif
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
	switch msg.SortOrder {
	case SortOrder_SORT_ORDER_DESC:
		filter.SortOrder = domain.SortOrderDesc
	case SortOrder_SORT_ORDER_ASC:
		filter.SortOrder = domain.SortOrderAsc
	}

	objects, nextCursor, total, err := h.svc.List(ctx, requestedTenantID, filter)
	if err != nil {
		return nil, mapError(err)
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot complete objects for other tenants"))
	}

	var etag *string
	if msg.Etag != "" {
		etag = &msg.Etag
	}

	// Use the intent-aware CompleteObjectByKey which first resolves from the
	// upload_intents table and falls back to the legacy objects table.
	updated, err := h.svc.CompleteObjectByKey(ctx, requestedTenantID, msg.Bucket, msg.Key, etag, nil)
	if err != nil {
		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("object upload completed", zap.String("object_id", updated.ID.String()))

	return connect.NewResponse(&CompleteObjectResponse{
		Object: objectToProto(updated),
	}), nil
}

func (h *ObjectHandler) RestoreObject(ctx context.Context, req *connect.Request[RestoreObjectRequest]) (*connect.Response[RestoreObjectResponse], error) {
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
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot restore objects of other tenants"))
	}

	rec, err := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if err != nil {
		return nil, mapError(err)
	}

	err = h.svc.Restore(ctx, requestedTenantID, rec.ID)
	if err != nil {
		return nil, mapError(err)
	}

	logger.FromContext(ctx).Info("object restored", zap.String("object_id", rec.ID.String()))

	// Re-fetch to get post-restore state (AVAILABLE status)
	updated, fetchErr := getObjectResiliently(ctx, h.svc, requestedTenantID, msg.Bucket, msg.Key)
	if fetchErr != nil {
		//nolint:nilerr
		return connect.NewResponse(&RestoreObjectResponse{
			Object: objectToProto(rec),
		}), nil
	}

	return connect.NewResponse(&RestoreObjectResponse{
		Object: objectToProto(updated),
	}), nil
}
