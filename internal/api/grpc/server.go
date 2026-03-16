package grpcapi

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is the unified gRPC + Connect RPC server implementation.
// It implements PaladinServiceServer and is used by both
// the gRPC server (directly) and the Connect handler (via generated adapter).
type Server struct {
	UnimplementedPaladinServiceServer
	log       *zap.Logger
	svc       domain.ObjectsService
	catSvc    domain.CategoryService
	tenantSvc domain.TenantService
}

// NewServer creates a new Server with the given service dependencies.
func NewServer(log *zap.Logger, svc domain.ObjectsService, catSvc domain.CategoryService, tenantSvc domain.TenantService) *Server {
	return &Server{log: log, svc: svc, catSvc: catSvc, tenantSvc: tenantSvc}
}

// tenantFromCtx reads the tenant ID set by the Auth interceptor chain.
// The internal (admin) API where the caller specifies tenant_id explicitly
// must use req.TenantId directly — these handlers are identified by their RPC name.
func tenantFromCtx(ctx context.Context) string {
	return utils.TenantIDFromContext(ctx, "")
}

// resolveTenant returns the tenant from context, falling back to the request-supplied tenant.
func resolveTenant(ctx context.Context, reqTenantID string) (string, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = reqTenantID
	}
	if tenant == "" {
		return "", status.Error(codes.Unauthenticated, "missing tenant context")
	}
	return tenant, nil
}

// ─── Object RPCs ─────────────────────────────────────────────────────────────

func (s *Server) CreateObject(ctx context.Context, req *CreateObjectRequest) (*CreateObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	var externalRef *string
	if req.ExternalRef != "" {
		externalRef = &req.ExternalRef
	}

	out, err := s.svc.CreateSingle(ctx, tenant, req.Category, req.ContentType, req.SizeBytes, req.Labels, externalRef, 0, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("CreateObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &CreateObjectResponse{
		ObjectId:      out.ID.String(),
		ObjectKey:     out.Key,
		UploadUrl:     out.Upload.URL,
		Method:        out.Upload.Method,
		Headers:       out.Upload.Headers,
		ExpiresAtUnix: out.Upload.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) GetObject(ctx context.Context, req *GetObjectRequest) (*GetObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	rec, err := s.svc.Get(ctx, tenant, id)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	var downloadURL string
	if rec.Status == domain.ObjectComplete {
		signed, signErr := s.svc.SignDownload(ctx, tenant, id, 3600)
		if signErr != nil {
			logger.FromContext(ctx).Warn("Failed to sign download URL for GetObject", zap.Error(signErr))
		} else {
			downloadURL = signed.URL
		}
	}

	var ref string
	if rec.ExternalRef != nil {
		ref = *rec.ExternalRef
	}

	return &GetObjectResponse{
		ObjectId:    rec.ID.String(),
		ObjectKey:   rec.ObjectKey,
		Bucket:      rec.Bucket,
		ContentType: rec.ContentType,
		SizeBytes:   rec.SizeBytes,
		Status:      string(rec.Status),
		DownloadUrl: downloadURL,
		Labels:      rec.Labels,
		ExternalRef: ref,
	}, nil
}

func (s *Server) GetObjectMeta(ctx context.Context, req *GetObjectMetaRequest) (*GetObjectMetaResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	rec, err := s.svc.GetMeta(ctx, tenant, id)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObjectMeta failed", zap.Error(err))

		return nil, grpcError(err)
	}

	var ref string
	if rec.ExternalRef != nil {
		ref = *rec.ExternalRef
	}

	var expires int64
	if rec.ExpiresAt != nil {
		expires = rec.ExpiresAt.Unix()
	}

	return &GetObjectMetaResponse{
		ObjectId:      rec.ID.String(),
		ObjectKey:     rec.ObjectKey,
		Bucket:        rec.Bucket,
		ContentType:   rec.ContentType,
		SizeBytes:     rec.SizeBytes,
		Status:        string(rec.Status),
		ExpiresAtUnix: expires,
		Labels:        rec.Labels,
		ExternalRef:   ref,
	}, nil
}

func (s *Server) PatchObjectMeta(ctx context.Context, req *PatchObjectMetaRequest) (*PatchObjectMetaResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	rec, err := s.svc.PatchMeta(ctx, tenant, id, req.Labels, req.ExternalRef)
	if err != nil {
		logger.FromContext(ctx).Warn("PatchObjectMeta failed", zap.Error(err))

		return nil, grpcError(err)
	}

	var ref string
	if rec.ExternalRef != nil {
		ref = *rec.ExternalRef
	}

	return &PatchObjectMetaResponse{
		ObjectId:    rec.ID.String(),
		Labels:      rec.Labels,
		ExternalRef: ref,
	}, nil
}

func (s *Server) CompleteObject(ctx context.Context, req *CompleteObjectRequest) (*CompleteObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if _, err := s.svc.CompleteObject(ctx, tenant, id, nil, nil); err != nil {
		logger.FromContext(ctx).Error("CompleteObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &CompleteObjectResponse{Status: "active"}, nil
}

func (s *Server) DeleteObject(ctx context.Context, req *DeleteObjectRequest) (*DeleteObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if err := s.svc.Delete(ctx, tenant, id); err != nil {
		logger.FromContext(ctx).Error("DeleteObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &DeleteObjectResponse{Status: "deleted"}, nil
}

func (s *Server) RestoreObject(ctx context.Context, req *RestoreObjectRequest) (*RestoreObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if err := s.svc.Restore(ctx, tenant, id); err != nil {
		logger.FromContext(ctx).Error("RestoreObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &RestoreObjectResponse{Status: "restored"}, nil
}

func (s *Server) PurgeObject(ctx context.Context, req *PurgeObjectRequest) (*PurgeObjectResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if err := s.svc.Purge(ctx, tenant, id, nil); err != nil {
		logger.FromContext(ctx).Error("PurgeObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &PurgeObjectResponse{Status: "purged"}, nil
}

// ─── Multipart RPCs ──────────────────────────────────────────────────────────

func (s *Server) InitiateMultipart(ctx context.Context, req *InitiateMultipartRequest) (*InitiateMultipartResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	var extRef *string
	if req.ExternalRef != "" {
		extRef = &req.ExternalRef
	}

	out, err := s.svc.InitiateMultipart(ctx, tenant, req.Category, req.ContentType, req.SizeBytes, req.Labels, extRef, 0, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("InitiateMultipart failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &InitiateMultipartResponse{
		ObjectId:      out.ObjectID.String(),
		ObjectKey:     out.ObjectKey,
		UploadId:      out.UploadID,
		PartSize:      out.PartSize,
		ExpiresAtUnix: out.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) SignPart(ctx context.Context, req *SignPartRequest) (*SignPartResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	p, err := s.svc.SignPart(ctx, tenant, req.UploadId, req.PartNumber)
	if err != nil {
		logger.FromContext(ctx).Warn("SignPart failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &SignPartResponse{
		UploadUrl:     p.URL,
		Method:        p.Method,
		ExpiresAtUnix: p.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) CompleteMultipart(ctx context.Context, req *CompleteMultipartRequest) (*CompleteMultipartResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	parts := make([]domain.CompletePart, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	rec, err := s.svc.CompleteMultipart(ctx, tenant, req.UploadId, parts)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteMultipart failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &CompleteMultipartResponse{ObjectId: rec.ID.String(), Status: "active"}, nil
}

func (s *Server) AbortMultipart(ctx context.Context, req *AbortMultipartRequest) (*AbortMultipartResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	if err := s.svc.AbortMultipart(ctx, tenant, req.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipart failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &AbortMultipartResponse{Status: "aborted"}, nil
}

// ─── Stats & List RPCs ───────────────────────────────────────────────────────

func (s *Server) GetObjectStats(ctx context.Context, req *GetObjectStatsRequest) (*GetObjectStatsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	stats, err := s.svc.GetStats(ctx, tenant)
	if err != nil {
		return nil, grpcError(err)
	}

	return &GetObjectStatsResponse{
		TotalCount:       stats.TotalCount,
		TotalSize:        stats.TotalSize,
		PendingCount:     stats.PendingCount,
		UploadingCount:   stats.UploadingCount,
		UploadedCount:    stats.UploadedCount,
		CompleteCount:    stats.CompleteCount,
		SoftDeletedCount: stats.SoftDeletedCount,
	}, nil
}

func (s *Server) ListObjects(ctx context.Context, req *ListObjectsRequest) (*ListObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	filter := domain.ListObjectsFilter{
		Category:  req.Category,
		KeyPrefix: req.Search,
		Recursive: req.Recursive,
	}

	if req.Status != nil {
		st := domain.ObjectStatus(*req.Status)
		filter.Status = &st
	}

	limit := safecast.IntFrom32(req.Limit)
	if limit <= 0 {
		limit = 20
	}

	filter.Limit = limit
	filter.Cursor = req.Cursor
	objects, nextCursor, total, err := s.svc.List(ctx, tenant, filter)
	if err != nil {
		return nil, grpcError(err)
	}

	respItems := make([]*ListObjectItem, 0, len(objects))
	for _, obj := range objects {
		var ref string
		if obj.ExternalRef != nil {
			ref = *obj.ExternalRef
		}

		var downloadURL string
		if obj.Status == domain.ObjectComplete {
			signed, signErr := s.svc.SignDownload(ctx, tenant, obj.ID, 3600)
			if signErr != nil {
				logger.FromContext(ctx).Warn("Failed to sign download URL for ListObjects item",
					zap.String("object_id", obj.ID.String()),
					zap.Error(signErr))
			} else {
				downloadURL = signed.URL
			}
		}

		respItems = append(respItems, &ListObjectItem{
			ObjectId:      obj.ID.String(),
			ObjectKey:     obj.ObjectKey,
			ContentType:   obj.ContentType,
			SizeBytes:     obj.SizeBytes,
			Status:        string(obj.Status),
			CreatedAtUnix: obj.CreatedAt.Unix(),
			Category:      obj.Category,
			Labels:        obj.Labels,
			ExternalRef:   ref,
			DownloadUrl:   downloadURL,
		})
	}

	return &ListObjectsResponse{
		Items:      respItems,
		NextCursor: nextCursor,
		TotalCount: total,
	}, nil
}

// ─── Category RPCs ───────────────────────────────────────────────────────────

func (s *Server) ListCategories(ctx context.Context, req *ListCategoriesRequest) (*ListCategoriesResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	filter := domain.ListCategoriesFilter{
		Limit:  safecast.IntFrom32(req.Limit),
		Cursor: req.Cursor,
		Search: req.Search,
	}
	if req.SortBy != nil {
		filter.SortBy = *req.SortBy
	}
	if req.SortOrder != nil {
		filter.SortOrder = *req.SortOrder
	}

	items, next, total, err := s.catSvc.List(ctx, tenant, filter)
	if err != nil {
		return nil, grpcError(err)
	}

	respItems := make([]*Category, len(items))
	for i, cat := range items {
		respItems[i] = &Category{
			Id:          cat.ID.String(),
			Slug:        cat.Slug,
			Name:        cat.Name,
			Description: cat.Description,
		}
	}

	return &ListCategoriesResponse{
		Items:      respItems,
		NextCursor: next,
		TotalCount: total,
	}, nil
}

func (s *Server) GetCategory(ctx context.Context, req *GetCategoryRequest) (*GetCategoryResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	cat, err := s.catSvc.Get(ctx, tenant, req.Slug)
	if err != nil {
		return nil, grpcError(err)
	}

	return &GetCategoryResponse{
		Category: &Category{
			Id:          cat.ID.String(),
			Slug:        cat.Slug,
			Name:        cat.Name,
			Description: cat.Description,
		},
	}, nil
}

func (s *Server) CreateCategory(ctx context.Context, req *CreateCategoryRequest) (*CreateCategoryResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	cat, err := s.catSvc.Create(ctx, tenant, req.Slug, req.Name, req.Description)
	if err != nil {
		return nil, grpcError(err)
	}

	return &CreateCategoryResponse{
		Category: &Category{
			Id:          cat.ID.String(),
			Slug:        cat.Slug,
			Name:        cat.Name,
			Description: cat.Description,
		},
	}, nil
}

func (s *Server) UpdateCategory(ctx context.Context, req *UpdateCategoryRequest) (*UpdateCategoryResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	cat, err := s.catSvc.Update(ctx, tenant, req.Slug, req.Name, req.Description)
	if err != nil {
		return nil, grpcError(err)
	}

	return &UpdateCategoryResponse{
		Category: &Category{
			Id:          cat.ID.String(),
			Slug:        cat.Slug,
			Name:        cat.Name,
			Description: cat.Description,
		},
	}, nil
}

func (s *Server) DeleteCategory(ctx context.Context, req *DeleteCategoryRequest) (*DeleteCategoryResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	if err := s.catSvc.Delete(ctx, tenant, req.Slug); err != nil {
		return nil, grpcError(err)
	}

	return &DeleteCategoryResponse{Status: "deleted"}, nil
}

func (s *Server) GetCategoryStats(ctx context.Context, req *GetCategoryStatsRequest) (*GetCategoryStatsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	stats, err := s.catSvc.GetStats(ctx, tenant, req.Slug)
	if err != nil {
		return nil, grpcError(err)
	}

	return &GetCategoryStatsResponse{
		TotalCount: stats.TotalCount,
		TotalSize:  stats.TotalSize,
	}, nil
}

// ─── Tenant Admin RPCs ───────────────────────────────────────────────────────

func (s *Server) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*CreateTenantResponse, error) {
	var displayName *string
	if req.DisplayName != "" {
		displayName = &req.DisplayName
	}

	t, err := s.tenantSvc.Create(ctx, req.TenantId, displayName, req.Labels, req.Tags)
	if err != nil {
		logger.FromContext(ctx).Warn("CreateTenant failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return toCreateTenantResponse(t), nil
}

func (s *Server) GetTenant(ctx context.Context, req *GetTenantRequest) (*GetTenantResponse, error) {
	t, err := s.tenantSvc.Get(ctx, req.TenantId)
	if err != nil {
		return nil, grpcError(err)
	}

	return toGetTenantResponse(t), nil
}

func (s *Server) DeleteTenant(ctx context.Context, req *DeleteTenantRequest) (*DeleteTenantResponse, error) {
	if err := s.tenantSvc.Delete(ctx, req.TenantId); err != nil {
		logger.FromContext(ctx).Warn("DeleteTenant failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &DeleteTenantResponse{Status: "deleted"}, nil
}

func (s *Server) ListTenants(ctx context.Context, req *ListTenantsRequest) (*ListTenantsResponse, error) {
	var tags []string
	if len(req.TagSelector) > 0 {
		tags = req.TagSelector
	}

	var cursor string
	if req.Cursor != "" {
		cursor = req.Cursor
	}

	limit := safecast.IntFrom32(req.Limit)
	if limit <= 0 {
		limit = 20
	}

	filter := domain.ListTenantsFilter{
		TagSelector: tags,
	}
	filter.Limit = limit
	filter.Cursor = cursor
	tenants, nextCursor, total, err := s.tenantSvc.List(ctx, filter)
	if err != nil {
		return nil, grpcError(err)
	}

	items := make([]*ListTenantsItem, 0, len(tenants))
	for i := range tenants {
		items = append(items, toListTenantsItem(&tenants[i]))
	}

	return &ListTenantsResponse{
		Items:      items,
		NextCursor: nextCursor,
		TotalCount: total,
	}, nil
}

func (s *Server) PatchTenantMetadata(ctx context.Context, req *PatchTenantMetadataRequest) (*PatchTenantMetadataResponse, error) {
	patch, err := parseLabelsPatch(req.LabelsPatchJson)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	t, err := s.tenantSvc.PatchMetadata(ctx, req.TenantId, patch, req.Tags, req.DisplayName)
	if err != nil {
		return nil, grpcError(err)
	}

	return toPatchTenantMetadataResponse(t), nil
}

// ─── Bulk RPCs ───────────────────────────────────────────────────────────────

func (s *Server) BulkCreateObjects(ctx context.Context, req *BulkCreateObjectsRequest) (*BulkCreateObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	items := make([]domain.CreateObjectRequest, len(req.Items))
	for i, item := range req.Items {
		var externalRef *string
		if item.ExternalRef != "" {
			externalRef = &item.ExternalRef
		}
		items[i] = domain.CreateObjectRequest{
			TenantID:    tenant,
			Category:    item.Category,
			ContentType: item.ContentType,
			SizeBytes:   item.SizeBytes,
			Labels:      item.Labels,
			ExternalRef: externalRef,
		}
	}

	var idempotencyKey *string
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		idempotencyKey = req.IdempotencyKey
	}

	res, err := s.svc.BulkCreate(ctx, tenant, items, idempotencyKey)
	if err != nil {
		return nil, grpcError(err)
	}

	respItems := make([]*CreateObjectResponse, len(res))
	for i, item := range res {
		respItems[i] = &CreateObjectResponse{
			ObjectId:      item.ID.String(),
			ObjectKey:     item.Key,
			UploadUrl:     item.Upload.URL,
			Method:        item.Upload.Method,
			Headers:       item.Upload.Headers,
			ExpiresAtUnix: item.Upload.ExpiresAt.Unix(),
		}
	}

	return &BulkCreateObjectsResponse{Items: respItems}, nil
}

func (s *Server) BulkDeleteObjects(ctx context.Context, req *BulkDeleteObjectsRequest) (*BulkDeleteObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	ids, err := parseUUIDs(req.ObjectIds)
	if err != nil {
		return nil, err
	}

	count, err := s.svc.BulkDelete(ctx, tenant, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkDeleteObjectsResponse{Count: count}, nil
}

func (s *Server) BulkRestoreObjects(ctx context.Context, req *BulkRestoreObjectsRequest) (*BulkRestoreObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	ids, err := parseUUIDs(req.ObjectIds)
	if err != nil {
		return nil, err
	}

	count, err := s.svc.BulkRestore(ctx, tenant, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkRestoreObjectsResponse{Count: count}, nil
}

func (s *Server) BulkPurgeObjects(ctx context.Context, req *BulkPurgeObjectsRequest) (*BulkPurgeObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	ids, err := parseUUIDs(req.ObjectIds)
	if err != nil {
		return nil, err
	}

	var idempotencyKey *string
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		idempotencyKey = req.IdempotencyKey
	}

	count, err := s.svc.BulkPurge(ctx, tenant, ids, idempotencyKey)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkPurgeObjectsResponse{Count: count}, nil
}

func (s *Server) BulkSignUploads(ctx context.Context, req *BulkSignUploadsRequest) (*BulkSignUploadsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	items := make([]domain.SignUploadItem, len(req.Items))
	for i, item := range req.Items {
		id, parseErr := uuid.Parse(item.ObjectId)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", item.ObjectId)
		}
		items[i] = domain.SignUploadItem{
			ObjectID:  id,
			UploadTTL: safecast.IntFrom32(item.UploadTtl),
		}
	}

	res, err := s.svc.BulkSignUploads(ctx, tenant, items)
	if err != nil {
		return nil, grpcError(err)
	}

	respItems := make([]*CreateObjectResponse, len(res))
	for i, item := range res {
		respItems[i] = &CreateObjectResponse{
			ObjectId:      item.ID.String(),
			ObjectKey:     item.Key,
			UploadUrl:     item.Upload.URL,
			Method:        item.Upload.Method,
			Headers:       item.Upload.Headers,
			ExpiresAtUnix: item.Upload.ExpiresAt.Unix(),
		}
	}

	return &BulkSignUploadsResponse{Items: respItems}, nil
}

func (s *Server) BulkCompleteObjects(ctx context.Context, req *BulkCompleteObjectsRequest) (*BulkCompleteObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	ids, err := parseUUIDs(req.ObjectIds)
	if err != nil {
		return nil, err
	}

	res, err := s.svc.BulkComplete(ctx, tenant, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	respItems := make([]*CompleteObjectResponse, len(res))
	for i := range res {
		respItems[i] = &CompleteObjectResponse{Status: "active"}
	}

	return &BulkCompleteObjectsResponse{Items: respItems}, nil
}

func (s *Server) BulkPatchObjects(ctx context.Context, req *BulkPatchObjectsRequest) (*BulkPatchObjectsResponse, error) {
	tenant, err := resolveTenant(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}

	items := make([]domain.BulkPatchItem, len(req.Items))
	for i, item := range req.Items {
		id, parseErr := uuid.Parse(item.ObjectId)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", item.ObjectId)
		}

		var extRef *string
		if item.ExternalRef != nil && *item.ExternalRef != "" {
			extRef = item.ExternalRef
		}

		items[i] = domain.BulkPatchItem{
			ID:          id,
			Labels:      item.Labels,
			ExternalRef: extRef,
		}
	}

	var idempotencyKey *string
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		idempotencyKey = req.IdempotencyKey
	}

	count, err := s.svc.BulkPatch(ctx, tenant, items, idempotencyKey)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkPatchObjectsResponse{Count: count}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// parseUUIDs converts a slice of string IDs to uuid.UUIDs, returning an error
// on the first invalid value.
func parseUUIDs(ids []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(ids))
	for _, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", idStr)
		}
		out = append(out, id)
	}

	return out, nil
}
