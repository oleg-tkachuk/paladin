package grpcapi

import (
	"context"

	"paladin/internal/service"
	"paladin/internal/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	log *zap.Logger
	svc *service.ObjectsService
}

func NewServer(log *zap.Logger, svc *service.ObjectsService) *Server {
	return &Server{log: log, svc: svc}
}

func (s *Server) CreateObject(ctx context.Context, req *CreateObjectRequest) (*CreateObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, key, p, err := s.svc.CreateSingle(ctx, tenant, req.ContentType, req.SizeBytes, nil)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &CreateObjectResponse{
		ObjectId:      id.String(),
		ObjectKey:     key,
		UploadUrl:     p.URL,
		Method:        p.Method,
		Headers:       p.Headers,
		ExpiresAtUnix: p.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) GetObject(ctx context.Context, req *GetObjectRequest) (*GetObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	rec, p, err := s.svc.Get(ctx, tenant, id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &GetObjectResponse{
		ObjectId:      rec.ID.String(),
		ObjectKey:     rec.ObjectKey,
		Bucket:        rec.Bucket,
		ContentType:   rec.ContentType,
		SizeBytes:     rec.SizeBytes,
		Status:        string(rec.Status),
		DownloadUrl:   p.URL,
		ExpiresAtUnix: p.ExpiresAt.Unix(),
	}, nil
}

func (s *Server) CompleteObject(ctx context.Context, req *CompleteObjectRequest) (*CompleteObjectResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	id, err := uuid.Parse(req.ObjectId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid object id")
	}

	if err := s.svc.MarkComplete(ctx, tenant, id); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &CompleteObjectResponse{Status: "active"}, nil
}

func (s *Server) InitiateMultipart(ctx context.Context, req *InitiateMultipartRequest) (*InitiateMultipartResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	out, err := s.svc.InitiateMultipart(ctx, tenant, req.ContentType, req.SizeBytes)
	if err != nil {
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

	parts := make([]service.CompletePart, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, service.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	objID, err := s.svc.CompleteMultipart(ctx, tenant, req.UploadId, parts)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &CompleteMultipartResponse{ObjectId: objID.String(), Status: "active"}, nil
}

func (s *Server) AbortMultipart(ctx context.Context, req *AbortMultipartRequest) (*AbortMultipartResponse, error) {
	tenant := req.TenantId
	if tenant == "" {
		tenant = utils.TenantIDFromContext(ctx, "default")
	}

	if err := s.svc.AbortMultipart(ctx, tenant, req.UploadId); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &AbortMultipartResponse{Status: "aborted"}, nil
}
