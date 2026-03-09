package grpcapi

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/google/uuid"
	publicapi "github.com/oleg-tkachuk/paladin/internal/api/grpc/public"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"connectrpc.com/connect"
)

// Server is the internal gRPC server implementation. It holds all service
// dependencies and implements PaladinServer.
type Server struct {
	UnimplementedPaladinServer
	log       *zap.Logger
	svc       domain.ObjectsService
	catSvc    domain.CategoryService
	tenantSvc domain.TenantService
}

// NewServer creates a new Server with the given service dependencies.
func NewServer(log *zap.Logger, svc domain.ObjectsService, catSvc domain.CategoryService, tenantSvc domain.TenantService) *Server {
	return &Server{log: log, svc: svc, catSvc: catSvc, tenantSvc: tenantSvc}
}

// NewConnectServer is a helper for the HTTP router to get a Connect-compatible
// implementation of the service.
func NewConnectServer(log *zap.Logger, svc domain.ObjectsService, catSvc domain.CategoryService, tenantSvc domain.TenantService) *ConnectServer {
	return &ConnectServer{Server: NewServer(log, svc, catSvc, tenantSvc)}
}

// ConnectServer wraps the internal gRPC Server to implement the Connect RPC interface.
type ConnectServer struct {
	*Server
}

func (s *ConnectServer) CreateObject(ctx context.Context, req *connect.Request[CreateObjectRequest]) (*connect.Response[CreateObjectResponse], error) {
	res, err := s.Server.CreateObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetObject(ctx context.Context, req *connect.Request[GetObjectRequest]) (*connect.Response[GetObjectResponse], error) {
	res, err := s.Server.GetObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetObjectMeta(ctx context.Context, req *connect.Request[GetObjectRequest]) (*connect.Response[GetObjectMetaResponse], error) {
	res, err := s.Server.GetObjectMeta(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) PatchObjectMeta(ctx context.Context, req *connect.Request[PatchObjectMetaRequest]) (*connect.Response[PatchObjectMetaResponse], error) {
	res, err := s.Server.PatchObjectMeta(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) CompleteObject(ctx context.Context, req *connect.Request[CompleteObjectRequest]) (*connect.Response[CompleteObjectResponse], error) {
	res, err := s.Server.CompleteObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) DeleteObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
	res, err := s.Server.DeleteObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) RestoreObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
	res, err := s.Server.RestoreObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) PurgeObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
	res, err := s.Server.PurgeObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) InitiateMultipart(ctx context.Context, req *connect.Request[InitiateMultipartRequest]) (*connect.Response[InitiateMultipartResponse], error) {
	res, err := s.Server.InitiateMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) SignPart(ctx context.Context, req *connect.Request[SignPartRequest]) (*connect.Response[SignPartResponse], error) {
	res, err := s.Server.SignPart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) CompleteMultipart(ctx context.Context, req *connect.Request[CompleteMultipartRequest]) (*connect.Response[CompleteMultipartResponse], error) {
	res, err := s.Server.CompleteMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) AbortMultipart(ctx context.Context, req *connect.Request[AbortMultipartRequest]) (*connect.Response[AbortMultipartResponse], error) {
	res, err := s.Server.AbortMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) ListCategories(ctx context.Context, req *connect.Request[ListCategoriesRequest]) (*connect.Response[ListCategoriesResponse], error) {
	res, err := s.Server.ListCategories(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetCategory(ctx context.Context, req *connect.Request[GetCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	res, err := s.Server.GetCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) CreateCategory(ctx context.Context, req *connect.Request[CreateCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	res, err := s.Server.CreateCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) UpdateCategory(ctx context.Context, req *connect.Request[UpdateCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	res, err := s.Server.UpdateCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) DeleteCategory(ctx context.Context, req *connect.Request[DeleteCategoryRequest]) (*connect.Response[DeleteCategoryResponse], error) {
	res, err := s.Server.DeleteCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetCategoryStats(ctx context.Context, req *connect.Request[GetCategoryStatsRequest]) (*connect.Response[GetCategoryStatsResponse], error) {
	res, err := s.Server.GetCategoryStats(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetObjectStats(ctx context.Context, req *connect.Request[GetObjectStatsRequest]) (*connect.Response[GetObjectStatsResponse], error) {
	res, err := s.Server.GetObjectStats(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) ListObjects(ctx context.Context, req *connect.Request[ListObjectsRequest]) (*connect.Response[ListObjectsResponse], error) {
	res, err := s.Server.ListObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) CreateTenant(ctx context.Context, req *connect.Request[CreateTenantRequest]) (*connect.Response[TenantResponse], error) {
	res, err := s.Server.CreateTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) GetTenant(ctx context.Context, req *connect.Request[GetTenantRequest]) (*connect.Response[TenantResponse], error) {
	res, err := s.Server.GetTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) DeleteTenant(ctx context.Context, req *connect.Request[DeleteTenantRequest]) (*connect.Response[DeleteTenantResponse], error) {
	res, err := s.Server.DeleteTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) ListTenants(ctx context.Context, req *connect.Request[ListTenantsRequest]) (*connect.Response[ListTenantsResponse], error) {
	res, err := s.Server.ListTenants(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) PatchTenantMetadata(ctx context.Context, req *connect.Request[PatchTenantMetadataRequest]) (*connect.Response[TenantResponse], error) {
	res, err := s.Server.PatchTenantMetadata(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkCreateObjects(ctx context.Context, req *connect.Request[BulkCreateObjectsRequest]) (*connect.Response[BulkCreateObjectsResponse], error) {
	res, err := s.Server.BulkCreateObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkDeleteObjects(ctx context.Context, req *connect.Request[BulkDeleteObjectsRequest]) (*connect.Response[BulkDeleteObjectsResponse], error) {
	res, err := s.Server.BulkDeleteObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkRestoreObjects(ctx context.Context, req *connect.Request[BulkRestoreObjectsRequest]) (*connect.Response[BulkRestoreObjectsResponse], error) {
	res, err := s.Server.BulkRestoreObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkPurgeObjects(ctx context.Context, req *connect.Request[BulkPurgeObjectsRequest]) (*connect.Response[BulkPurgeObjectsResponse], error) {
	res, err := s.Server.BulkPurgeObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkSignUploads(ctx context.Context, req *connect.Request[BulkSignUploadsRequest]) (*connect.Response[BulkSignUploadsResponse], error) {
	res, err := s.Server.BulkSignUploads(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkCompleteObjects(ctx context.Context, req *connect.Request[BulkCompleteObjectsRequest]) (*connect.Response[BulkCompleteObjectsResponse], error) {
	res, err := s.Server.BulkCompleteObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *ConnectServer) BulkPatchObjects(ctx context.Context, req *connect.Request[BulkPatchObjectsRequest]) (*connect.Response[BulkPatchObjectsResponse], error) {
	res, err := s.Server.BulkPatchObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// PublicServer wraps the internal Server to satisfy the public API interface.
type PublicServer struct {
	publicapi.UnimplementedPaladinServer
	*Server
}

// tenantFromCtx reads the tenant ID set by the Auth interceptor chain.
// The internal (admin) API where the caller specifies tenant_id explicitly
// must use req.TenantId directly — these handlers are identified by their RPC name.
func tenantFromCtx(ctx context.Context) string {
	return utils.TenantIDFromContext(ctx, "")
}

// ─── Object RPCs ─────────────────────────────────────────────────────────────

func (s *Server) CreateObject(ctx context.Context, req *CreateObjectRequest) (*CreateObjectResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
		// Generate signed URL (default 1 hour TTL)
		// TODO: In a real app, this TTL might be configurable or passed in the request
		signed, err := s.svc.SignDownload(ctx, tenant, id, 3600)
		if err != nil {
			logger.FromContext(ctx).Warn("Failed to sign download URL for GetObject", zap.Error(err))
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

func (s *Server) GetObjectMeta(ctx context.Context, req *GetObjectRequest) (*GetObjectMetaResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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

func (s *Server) PurgeObject(ctx context.Context, req *DeleteObjectRequest) (*DeleteObjectResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if err := s.svc.Purge(ctx, tenant, id, nil); err != nil {
		logger.FromContext(ctx).Error("PurgeObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &DeleteObjectResponse{Status: "purged"}, nil
}

func (s *Server) RestoreObject(ctx context.Context, req *DeleteObjectRequest) (*DeleteObjectResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "object_id must be a valid UUID")
	}

	if err := s.svc.Restore(ctx, tenant, id); err != nil {
		logger.FromContext(ctx).Error("RestoreObject failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &DeleteObjectResponse{Status: "restored"}, nil
}

// ─── Multipart RPCs ──────────────────────────────────────────────────────────

func (s *Server) InitiateMultipart(ctx context.Context, req *InitiateMultipartRequest) (*InitiateMultipartResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	if err := s.svc.AbortMultipart(ctx, tenant, req.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipart failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return &AbortMultipartResponse{Status: "aborted"}, nil
}

// ─── Stats & List RPCs ───────────────────────────────────────────────────────

func (s *Server) GetObjectStats(ctx context.Context, req *GetObjectStatsRequest) (*GetObjectStatsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
			// Generate signed URL (default 1 hour TTL)
			signed, err := s.svc.SignDownload(ctx, tenant, obj.ID, 3600)
			if err != nil {
				logger.FromContext(ctx).Warn("Failed to sign download URL for ListObjects item",
					zap.String("object_id", obj.ID.String()),
					zap.Error(err))
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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

func (s *Server) CreateCategory(ctx context.Context, req *CreateCategoryRequest) (*GetCategoryResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	cat, err := s.catSvc.Create(ctx, tenant, req.Slug, req.Name, req.Description)
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

func (s *Server) UpdateCategory(ctx context.Context, req *UpdateCategoryRequest) (*GetCategoryResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	cat, err := s.catSvc.Update(ctx, tenant, req.Slug, req.Name, req.Description)
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

func (s *Server) DeleteCategory(ctx context.Context, req *DeleteCategoryRequest) (*DeleteCategoryResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	if err := s.catSvc.Delete(ctx, tenant, req.Slug); err != nil {
		return nil, grpcError(err)
	}

	return &DeleteCategoryResponse{Status: "deleted"}, nil
}

func (s *Server) GetCategoryStats(ctx context.Context, req *GetCategoryStatsRequest) (*GetCategoryStatsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
// These RPCs are only accessible on the internal gRPC server.
// Use the AdminOnly interceptor at the server level or configure the client
// to supply the admin bearer key.

func (s *Server) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*TenantResponse, error) {
	var displayName *string
	if req.DisplayName != "" {
		displayName = &req.DisplayName
	}

	t, err := s.tenantSvc.Create(ctx, req.TenantId, displayName, req.Labels, req.Tags)
	if err != nil {
		logger.FromContext(ctx).Warn("CreateTenant failed", zap.Error(err))

		return nil, grpcError(err)
	}

	return domainTenantToProto(t), nil
}

func (s *Server) GetTenant(ctx context.Context, req *GetTenantRequest) (*TenantResponse, error) {
	t, err := s.tenantSvc.Get(ctx, req.TenantId)
	if err != nil {
		return nil, grpcError(err)
	}

	return domainTenantToProto(t), nil
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

	items := make([]*TenantResponse, 0, len(tenants))
	for i := range tenants {
		items = append(items, domainTenantToProto(&tenants[i]))
	}

	return &ListTenantsResponse{
		Items:      items,
		NextCursor: nextCursor,
		TotalCount: total,
	}, nil
}

func (s *Server) BulkCreateObjects(ctx context.Context, req *BulkCreateObjectsRequest) (*BulkCreateObjectsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	ids := make([]uuid.UUID, 0, len(req.ObjectIds))
	for _, idStr := range req.ObjectIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", idStr)
		}
		ids = append(ids, id)
	}

	count, err := s.svc.BulkDelete(ctx, tenant, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkDeleteObjectsResponse{Count: count}, nil
}

func (s *Server) BulkRestoreObjects(ctx context.Context, req *BulkRestoreObjectsRequest) (*BulkRestoreObjectsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	ids := make([]uuid.UUID, 0, len(req.ObjectIds))
	for _, idStr := range req.ObjectIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", idStr)
		}
		ids = append(ids, id)
	}

	count, err := s.svc.BulkRestore(ctx, tenant, ids)
	if err != nil {
		return nil, grpcError(err)
	}

	return &BulkRestoreObjectsResponse{Count: count}, nil
}

func (s *Server) BulkPurgeObjects(ctx context.Context, req *BulkPurgeObjectsRequest) (*BulkPurgeObjectsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	ids := make([]uuid.UUID, 0, len(req.ObjectIds))
	for _, idStr := range req.ObjectIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", idStr)
		}
		ids = append(ids, id)
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	items := make([]domain.SignUploadItem, len(req.Items))
	for i, item := range req.Items {
		id, err := uuid.Parse(item.ObjectId)
		if err != nil {
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	ids := make([]uuid.UUID, 0, len(req.ObjectIds))
	for _, idStr := range req.ObjectIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid object_id: %s", idStr)
		}
		ids = append(ids, id)
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
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		tenant = req.TenantId
	}
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	items := make([]domain.BulkPatchItem, len(req.Items))
	for i, item := range req.Items {
		id, err := uuid.Parse(item.ObjectId)
		if err != nil {
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

func (s *Server) PatchTenantMetadata(ctx context.Context, req *PatchTenantMetadataRequest) (*TenantResponse, error) {
	patch, err := parseLabelsPatch(req.LabelsPatchJson)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	t, err := s.tenantSvc.PatchMetadata(ctx, req.TenantId, patch, req.Tags, req.DisplayName)
	if err != nil {
		return nil, grpcError(err)
	}

	return domainTenantToProto(t), nil
}

// domainTenantToProto converts a domain.Tenant to a TenantResponse proto message.
func domainTenantToProto(t *domain.Tenant) *TenantResponse {
	displayName := ""
	if t.DisplayName != nil {
		displayName = *t.DisplayName
	}

	labels := t.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}

	return &TenantResponse{
		TenantId:      t.TenantID,
		DisplayName:   displayName,
		Labels:        labels,
		Tags:          tags,
		CreatedAtUnix: t.CreatedAt.Unix(),
		UpdatedAtUnix: t.UpdatedAt.Unix(),
	}
}

// ─── Public server wrappers ───────────────────────────────────────────────────
// All public-api methods delegate to the internal Server, which now reads
// the tenant from context (set by the Auth interceptor) rather than from
// the request body. The public proto messages don't include tenant_id.

func (s *PublicServer) ListObjects(ctx context.Context, req *publicapi.ListObjectsRequest) (*publicapi.ListObjectsResponse, error) {
	tenant := tenantFromCtx(ctx)
	if tenant == "" {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	filter := domain.ListObjectsFilter{
		Category:  req.Category,
		Search:    req.Search,
		Recursive: req.Recursive,
		Limit:     safecast.IntFrom32(req.Limit),
		Cursor:    req.Cursor,
		SortBy:    req.GetSortBy(),
		SortOrder: req.GetSortOrder(),
	}

	if statusStr := req.GetStatus(); statusStr != "" {
		st := domain.ObjectStatus(statusStr)
		filter.Status = &st
	}

	resp, nextCursor, totalCount, err := s.svc.List(ctx, tenant, filter)
	if err != nil {
		return nil, s.Server.mapError(err)
	}

	items := make([]*publicapi.ListObjectItem, len(resp))
	for i, item := range resp {
		extRef := ""
		if item.ExternalRef != nil {
			extRef = *item.ExternalRef
		}
		items[i] = &publicapi.ListObjectItem{
			ObjectId:      item.ID.String(),
			ObjectKey:     item.ObjectKey,
			ContentType:   item.ContentType,
			SizeBytes:     item.SizeBytes,
			Status:        string(item.Status),
			CreatedAtUnix: item.CreatedAt.Unix(),
			Category:      item.Category,
			Labels:        item.Labels,
			ExternalRef:   extRef,
		}
	}

	return &publicapi.ListObjectsResponse{
		Items:      items,
		NextCursor: nextCursor,
		TotalCount: totalCount,
	}, nil
}

func (s *PublicServer) CreateObject(ctx context.Context, req *publicapi.CreateObjectRequest) (*publicapi.CreateObjectResponse, error) {
	internalReq := &CreateObjectRequest{
		Category:    req.Category,
		ContentType: req.ContentType,
		SizeBytes:   req.SizeBytes,
		Labels:      req.Labels,
		ExternalRef: req.ExternalRef,
	}
	resp, err := s.Server.CreateObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.CreateObjectResponse{
		ObjectId:      resp.ObjectId,
		ObjectKey:     resp.ObjectKey,
		UploadUrl:     resp.UploadUrl,
		Method:        resp.Method,
		Headers:       resp.Headers,
		ExpiresAtUnix: resp.ExpiresAtUnix,
	}, nil
}

func (s *PublicServer) GetObject(ctx context.Context, req *publicapi.GetObjectRequest) (*publicapi.GetObjectResponse, error) {
	internalReq := &GetObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.GetObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetObjectResponse{
		ObjectId:    resp.ObjectId,
		ObjectKey:   resp.ObjectKey,
		Bucket:      resp.Bucket,
		ContentType: resp.ContentType,
		SizeBytes:   resp.SizeBytes,
		Status:      resp.Status,
		Labels:      resp.Labels,
		ExternalRef: resp.ExternalRef,
	}, nil
}

func (s *PublicServer) GetObjectMeta(ctx context.Context, req *publicapi.GetObjectRequest) (*publicapi.GetObjectMetaResponse, error) {
	internalReq := &GetObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.GetObjectMeta(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetObjectMetaResponse{
		ObjectId:      resp.ObjectId,
		ObjectKey:     resp.ObjectKey,
		Bucket:        resp.Bucket,
		ContentType:   resp.ContentType,
		SizeBytes:     resp.SizeBytes,
		Status:        resp.Status,
		ExpiresAtUnix: resp.ExpiresAtUnix,
		Labels:        resp.Labels,
		ExternalRef:   resp.ExternalRef,
	}, nil
}

func (s *PublicServer) CompleteObject(ctx context.Context, req *publicapi.CompleteObjectRequest) (*publicapi.CompleteObjectResponse, error) {
	internalReq := &CompleteObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.CompleteObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.CompleteObjectResponse{Status: resp.Status}, nil
}

func (s *PublicServer) DeleteObject(ctx context.Context, req *publicapi.DeleteObjectRequest) (*publicapi.DeleteObjectResponse, error) {
	internalReq := &DeleteObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.DeleteObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.DeleteObjectResponse{Status: resp.Status}, nil
}

func (s *PublicServer) RestoreObject(ctx context.Context, req *publicapi.DeleteObjectRequest) (*publicapi.DeleteObjectResponse, error) {
	internalReq := &DeleteObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.RestoreObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.DeleteObjectResponse{Status: resp.Status}, nil
}

func (s *PublicServer) PurgeObject(ctx context.Context, req *publicapi.DeleteObjectRequest) (*publicapi.DeleteObjectResponse, error) {
	internalReq := &DeleteObjectRequest{ObjectId: req.ObjectId}
	resp, err := s.Server.PurgeObject(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.DeleteObjectResponse{Status: resp.Status}, nil
}

func (s *PublicServer) InitiateMultipart(ctx context.Context, req *publicapi.InitiateMultipartRequest) (*publicapi.InitiateMultipartResponse, error) {
	internalReq := &InitiateMultipartRequest{
		Category:    req.Category,
		ContentType: req.ContentType,
		SizeBytes:   req.SizeBytes,
		Labels:      req.Labels,
		ExternalRef: req.ExternalRef,
	}
	resp, err := s.Server.InitiateMultipart(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.InitiateMultipartResponse{
		ObjectId:      resp.ObjectId,
		ObjectKey:     resp.ObjectKey,
		UploadId:      resp.UploadId,
		PartSize:      resp.PartSize,
		ExpiresAtUnix: resp.ExpiresAtUnix,
	}, nil
}

func (s *PublicServer) SignPart(ctx context.Context, req *publicapi.SignPartRequest) (*publicapi.SignPartResponse, error) {
	internalReq := &SignPartRequest{UploadId: req.UploadId, PartNumber: req.PartNumber}
	resp, err := s.Server.SignPart(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.SignPartResponse{
		UploadUrl:     resp.UploadUrl,
		Method:        resp.Method,
		ExpiresAtUnix: resp.ExpiresAtUnix,
	}, nil
}

func (s *PublicServer) CompleteMultipart(ctx context.Context, req *publicapi.CompleteMultipartRequest) (*publicapi.CompleteMultipartResponse, error) {
	parts := make([]*CompleteMultipartPart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = &CompleteMultipartPart{PartNumber: p.PartNumber, Etag: p.Etag}
	}
	internalReq := &CompleteMultipartRequest{UploadId: req.UploadId, Parts: parts}
	resp, err := s.Server.CompleteMultipart(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.CompleteMultipartResponse{ObjectId: resp.ObjectId, Status: resp.Status}, nil
}

func (s *PublicServer) AbortMultipart(ctx context.Context, req *publicapi.AbortMultipartRequest) (*publicapi.AbortMultipartResponse, error) {
	internalReq := &AbortMultipartRequest{UploadId: req.UploadId}
	resp, err := s.Server.AbortMultipart(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.AbortMultipartResponse{Status: resp.Status}, nil
}

func (s *PublicServer) ListCategories(ctx context.Context, req *publicapi.ListCategoriesRequest) (*publicapi.ListCategoriesResponse, error) {
	tenantID := tenantFromCtx(ctx)
	if tenantID == "" {
		return nil, status.Error(codes.Unauthenticated, "tenant ID is required")
	}

	filter := domain.ListCategoriesFilter{
		Limit:     safecast.IntFrom32(req.GetLimit()),
		Cursor:    req.GetCursor(),
		Search:    req.Search,
		SortBy:    req.GetSortBy(),
		SortOrder: req.GetSortOrder(),
	}

	items, next, total, err := s.catSvc.List(ctx, tenantID, filter)
	if err != nil {
		return nil, s.Server.mapError(err)
	}

	categories := make([]*publicapi.Category, len(items))
	for i, item := range items {
		categories[i] = &publicapi.Category{
			Id:          item.ID.String(),
			Slug:        item.Slug,
			Name:        item.Name,
			Description: item.Description,
		}
	}

	return &publicapi.ListCategoriesResponse{
		Items:      categories,
		NextCursor: next,
		TotalCount: total,
	}, nil
}

func (s *Server) mapError(err error) error {
	return status.Error(codes.Internal, err.Error())
}

func ptr[T any](v T) *T {
	return &v
}

func (s *PublicServer) GetCategory(ctx context.Context, req *publicapi.GetCategoryRequest) (*publicapi.GetCategoryResponse, error) {
	internalReq := &GetCategoryRequest{Slug: req.Slug}
	resp, err := s.Server.GetCategory(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetCategoryResponse{
		Category: &publicapi.Category{
			Id:          resp.Category.Id,
			Slug:        resp.Category.Slug,
			Name:        resp.Category.Name,
			Description: resp.Category.Description,
		},
	}, nil
}

func (s *PublicServer) CreateCategory(ctx context.Context, req *publicapi.CreateCategoryRequest) (*publicapi.GetCategoryResponse, error) {
	internalReq := &CreateCategoryRequest{Slug: req.Slug, Name: req.Name, Description: req.Description}
	resp, err := s.Server.CreateCategory(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetCategoryResponse{
		Category: &publicapi.Category{
			Id:          resp.Category.Id,
			Slug:        resp.Category.Slug,
			Name:        resp.Category.Name,
			Description: resp.Category.Description,
		},
	}, nil
}

func (s *PublicServer) DeleteCategory(ctx context.Context, req *publicapi.DeleteCategoryRequest) (*publicapi.DeleteCategoryResponse, error) {
	internalReq := &DeleteCategoryRequest{Slug: req.Slug}
	resp, err := s.Server.DeleteCategory(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.DeleteCategoryResponse{Status: resp.Status}, nil
}

func (s *PublicServer) GetCategoryStats(ctx context.Context, req *publicapi.GetCategoryStatsRequest) (*publicapi.GetCategoryStatsResponse, error) {
	internalReq := &GetCategoryStatsRequest{Slug: req.Slug}
	resp, err := s.Server.GetCategoryStats(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetCategoryStatsResponse{TotalCount: resp.TotalCount, TotalSize: resp.TotalSize}, nil
}

func (s *PublicServer) GetObjectStats(ctx context.Context, req *publicapi.GetObjectStatsRequest) (*publicapi.GetObjectStatsResponse, error) {
	internalReq := &GetObjectStatsRequest{}
	resp, err := s.Server.GetObjectStats(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	return &publicapi.GetObjectStatsResponse{
		TotalCount:       resp.TotalCount,
		TotalSize:        resp.TotalSize,
		PendingCount:     resp.PendingCount,
		UploadingCount:   resp.UploadingCount,
		UploadedCount:    resp.UploadedCount,
		CompleteCount:    resp.CompleteCount,
		SoftDeletedCount: resp.SoftDeletedCount,
	}, nil
}
