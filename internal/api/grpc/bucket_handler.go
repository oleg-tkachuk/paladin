package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"google.golang.org/grpc"
)

// BucketHandler implements grpcapiconnect.BucketServiceHandler.
type BucketHandler struct {
	log  *zap.Logger
	repo domain.ObjectsService
}

// NewBucketHandler creates a new BucketHandler.
func NewBucketHandler(log *zap.Logger, repo domain.ObjectsService) *BucketHandler {
	return &BucketHandler{
		log:  log,
		repo: repo,
	}
}

func (h *BucketHandler) CreateBucket(ctx context.Context, req *connect.Request[CreateBucketRequest]) (*connect.Response[CreateBucketResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) DeleteBucket(ctx context.Context, req *connect.Request[DeleteBucketRequest]) (*connect.Response[DeleteBucketResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) ListBuckets(ctx context.Context, req *connect.Request[ListBucketsRequest]) (*connect.Response[ListBucketsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) GetBucketConfiguration(ctx context.Context, req *connect.Request[GetBucketConfigurationRequest]) (*connect.Response[GetBucketConfigurationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) UpdateBucketConfiguration(ctx context.Context, req *connect.Request[UpdateBucketConfigurationRequest]) (*connect.Response[UpdateBucketConfigurationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) GetBucketStats(ctx context.Context, req *connect.Request[GetBucketStatsRequest]) (*connect.Response[GetBucketStatsResponse], error) {
	objects, size, err := h.repo.GetBucketStats(ctx, req.Msg.TenantId, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&GetBucketStatsResponse{
		TotalObjects:   objects,
		TotalSizeBytes: size,
	}), nil
}

// ────────────────────────────────────────────────────────────────────────────
// gRPC Bridge
// ────────────────────────────────────────────────────────────────────────────

type bucketGRPCServer struct {
	UnimplementedBucketServiceServer
	h *BucketHandler
}

// RegisterGRPC registers the handler as a native gRPC server.
func (h *BucketHandler) RegisterGRPC(srv *grpc.Server) {
	RegisterBucketServiceServer(srv, &bucketGRPCServer{h: h})
}

func (s *bucketGRPCServer) CreateBucket(ctx context.Context, req *CreateBucketRequest) (*CreateBucketResponse, error) {
	res, err := s.h.CreateBucket(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *bucketGRPCServer) DeleteBucket(ctx context.Context, req *DeleteBucketRequest) (*DeleteBucketResponse, error) {
	res, err := s.h.DeleteBucket(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *bucketGRPCServer) ListBuckets(ctx context.Context, req *ListBucketsRequest) (*ListBucketsResponse, error) {
	res, err := s.h.ListBuckets(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *bucketGRPCServer) GetBucketConfiguration(ctx context.Context, req *GetBucketConfigurationRequest) (*GetBucketConfigurationResponse, error) {
	res, err := s.h.GetBucketConfiguration(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *bucketGRPCServer) UpdateBucketConfiguration(ctx context.Context, req *UpdateBucketConfigurationRequest) (*UpdateBucketConfigurationResponse, error) {
	res, err := s.h.UpdateBucketConfiguration(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *bucketGRPCServer) GetBucketStats(ctx context.Context, req *GetBucketStatsRequest) (*GetBucketStatsResponse, error) {
	res, err := s.h.GetBucketStats(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}
