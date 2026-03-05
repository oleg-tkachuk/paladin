package grpcapi

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"github.com/google/uuid"
	publicapi "github.com/oleg-tkachuk/paladin/internal/api/grpc/public"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	UnimplementedPaladinServer
	log    *zap.Logger
	svc    domain.ObjectsService
	catSvc domain.CategoryService
}

func NewServer(log *zap.Logger, svc domain.ObjectsService, catSvc domain.CategoryService) *Server {
	return &Server{log: log, svc: svc, catSvc: catSvc}
}

// PublicServer wraps the internal Server to satisfy the public API interface.
type PublicServer struct {
	publicapi.UnimplementedPaladinServer
	*Server
}

func (s *PublicServer) ListObjects(ctx context.Context, req *publicapi.ListObjectsRequest) (*publicapi.ListObjectsResponse, error) {
	internalReq := &ListObjectsRequest{
		Category: req.Category,
		Status:   req.Status,
		Limit:    req.Limit,
		Cursor:   req.Cursor,
		Search:   req.Search,
	}
	resp, err := s.Server.ListObjects(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	items := make([]*publicapi.ListObjectItem, len(resp.Items))
	for i, item := range resp.Items {
		items[i] = &publicapi.ListObjectItem{
			ObjectId:      item.ObjectId,
			ObjectKey:     item.ObjectKey,
			ContentType:   item.ContentType,
			SizeBytes:     item.SizeBytes,
			Status:        item.Status,
			CreatedAtUnix: item.CreatedAtUnix,
			Category:      item.Category,
			Labels:        item.Labels,
			ExternalRef:   item.ExternalRef,
		}
	}

	return &publicapi.ListObjectsResponse{
		Items:      items,
		NextCursor: resp.NextCursor,
		TotalCount: resp.TotalCount,
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
	internalReq := &ListCategoriesRequest{Limit: req.Limit, Cursor: req.Cursor}
	resp, err := s.Server.ListCategories(ctx, internalReq)
	if err != nil {
		return nil, err
	}
	items := make([]*publicapi.Category, len(resp.Items))
	for i, item := range resp.Items {
		items[i] = &publicapi.Category{
			Id:          item.Id,
			Slug:        item.Slug,
			Name:        item.Name,
			Description: item.Description,
		}
	}
	return &publicapi.ListCategoriesResponse{Items: items, NextCursor: resp.NextCursor, TotalCount: resp.TotalCount}, nil
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

func (s *Server) ListCategories(ctx context.Context, req *ListCategoriesRequest) (*ListCategoriesResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	items, next, total, err := s.catSvc.List(ctx, tenant, int(req.Limit), req.Cursor)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	cat, err := s.catSvc.Get(ctx, tenant, req.Slug)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
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
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	cat, err := s.catSvc.Create(ctx, tenant, req.Slug, req.Name, req.Description)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	err := s.catSvc.Delete(ctx, tenant, req.Slug)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &DeleteCategoryResponse{Status: "deleted"}, nil
}

func (s *Server) GetCategoryStats(ctx context.Context, req *GetCategoryStatsRequest) (*GetCategoryStatsResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	stats, err := s.catSvc.GetStats(ctx, tenant, req.Slug)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &GetCategoryStatsResponse{
		TotalCount: stats.TotalCount,
		TotalSize:  stats.TotalSize,
	}, nil
}

func (s *Server) CreateObject(ctx context.Context, req *CreateObjectRequest) (*CreateObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	var externalRef *string
	if req.ExternalRef != "" {
		externalRef = &req.ExternalRef
	}

	category := req.Category

	out, err := s.svc.CreateSingle(ctx, tenant, category, req.ContentType, req.SizeBytes, req.Labels, externalRef, 0, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("CreateObject failed", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, err.Error())
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
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObject invalid ID", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	rec, err := s.svc.Get(ctx, tenant, id)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObject failed", zap.Error(err))

		return nil, status.Error(codes.NotFound, err.Error())
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
		Labels:      rec.Labels,
		ExternalRef: ref,
	}, nil
}

func (s *Server) GetObjectMeta(ctx context.Context, req *GetObjectRequest) (*GetObjectMetaResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObjectMeta invalid ID", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	rec, err := s.svc.GetMeta(ctx, tenant, id)
	if err != nil {
		logger.FromContext(ctx).Warn("GetObjectMeta failed", zap.Error(err))

		return nil, status.Error(codes.NotFound, err.Error())
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

func (s *Server) CompleteObject(ctx context.Context, req *CompleteObjectRequest) (*CompleteObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteObject invalid ID", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	if _, err := s.svc.CompleteObject(ctx, tenant, id, nil, nil); err != nil {
		logger.FromContext(ctx).Error("CompleteObject failed", zap.Error(err))

		return nil, status.Error(codes.Internal, err.Error())
	}

	return &CompleteObjectResponse{Status: "active"}, nil
}

func (s *Server) InitiateMultipart(ctx context.Context, req *InitiateMultipartRequest) (*InitiateMultipartResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	category := req.Category
	// category will be validated in the service layer

	var extRef *string
	if req.ExternalRef != "" {
		extRef = &req.ExternalRef
	}

	out, err := s.svc.InitiateMultipart(ctx, tenant, category, req.ContentType, req.SizeBytes, req.Labels, extRef, 0, nil)
	if err != nil {
		logger.FromContext(ctx).Warn("InitiateMultipart failed", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, err.Error())
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
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	p, err := s.svc.SignPart(ctx, tenant, req.UploadId, req.PartNumber)
	if err != nil {
		logger.FromContext(ctx).Warn("SignPart failed", zap.Error(err))

		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &SignPartResponse{
		UploadUrl:     p.URL,
		Method:        p.Method,
		ExpiresAtUnix: p.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) CompleteMultipart(ctx context.Context, req *CompleteMultipartRequest) (*CompleteMultipartResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	parts := make([]domain.CompletePart, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	rec, err := s.svc.CompleteMultipart(ctx, tenant, req.UploadId, parts)
	if err != nil {
		logger.FromContext(ctx).Warn("CompleteMultipart failed", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &CompleteMultipartResponse{ObjectId: rec.ID.String(), Status: "active"}, nil
}

func (s *Server) AbortMultipart(ctx context.Context, req *AbortMultipartRequest) (*AbortMultipartResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	if err := s.svc.AbortMultipart(ctx, tenant, req.UploadId); err != nil {
		logger.FromContext(ctx).Warn("AbortMultipart failed", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &AbortMultipartResponse{Status: "aborted"}, nil
}

func (s *Server) GetObjectStats(ctx context.Context, req *GetObjectStatsRequest) (*GetObjectStatsResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	stats, err := s.svc.GetStats(ctx, tenant)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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

func (s *Server) DeleteObject(ctx context.Context, req *DeleteObjectRequest) (*DeleteObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		logger.FromContext(ctx).Warn("DeleteObject invalid ID", zap.Error(err))

		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	if err := s.svc.Purge(ctx, tenant, id, nil); err != nil {
		logger.FromContext(ctx).Error("DeleteObject failed", zap.Error(err))

		return nil, status.Error(codes.Internal, err.Error())
	}

	return &DeleteObjectResponse{Status: "deleted"}, nil
}

func (s *Server) ListObjects(ctx context.Context, req *ListObjectsRequest) (*ListObjectsResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	filter := domain.ListObjectsFilter{
		Category:  req.Category,
		KeyPrefix: req.Search,
	}

	if req.Status != nil {
		st := domain.ObjectStatus(*req.Status)
		filter.Status = &st
	}

	items, next, total, err := s.svc.List(ctx, tenant, filter, int(req.Limit), req.Cursor)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	respItems := make([]*ListObjectItem, len(items))
	for i, obj := range items {
		var ref string
		if obj.ExternalRef != nil {
			ref = *obj.ExternalRef
		}

		respItems[i] = &ListObjectItem{
			ObjectId:      obj.ID.String(),
			ObjectKey:     obj.ObjectKey,
			ContentType:   obj.ContentType,
			SizeBytes:     obj.SizeBytes,
			Status:        string(obj.Status),
			CreatedAtUnix: obj.CreatedAt.Unix(),
			Category:      obj.Category,
			Labels:        obj.Labels,
			ExternalRef:   ref,
		}
	}

	return &ListObjectsResponse{
		Items:      respItems,
		NextCursor: next,
		TotalCount: total,
	}, nil
}
